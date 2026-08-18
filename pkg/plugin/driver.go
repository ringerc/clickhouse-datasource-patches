package plugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/grafana/clickhouse-datasource/pkg/converters"
	"github.com/grafana/clickhouse-datasource/pkg/macros"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	sdkproxy "github.com/grafana/grafana-plugin-sdk-go/backend/proxy"
	"github.com/grafana/grafana-plugin-sdk-go/backend/tracing"
	"github.com/grafana/grafana-plugin-sdk-go/build/buildinfo"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	schemas "github.com/grafana/schemads"
	"github.com/grafana/sqlds/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/proxy"
)

type grafanaHeadersKeyType struct{}

var grafanaHeadersKey = grafanaHeadersKeyType{}

// resolvedEnforcedCtxKeyType is the key type for the per-request resolved
// clickhouse.Settings attached by MutateQueryData after binding resolution.
type resolvedEnforcedCtxKeyType struct{}

var resolvedEnforcedCtxKey = resolvedEnforcedCtxKeyType{}

// resolvedEnforcedErrCtxKeyType is the key type for a binding-resolution error
// smuggled from MutateQueryData to interpolateMacros so the query is rejected
// before it reaches the database.
type resolvedEnforcedErrCtxKeyType struct{}

var resolvedEnforcedErrCtxKey = resolvedEnforcedErrCtxKeyType{}

type grafanaHeaders struct {
	DashboardUID string
	PanelID      string
	RuleUID      string
}

// enforcedSettingsCtxKeyType is the key type for storing the enforced
// clickhouse.Settings in the context, used by tests to verify attachment.
type enforcedSettingsCtxKeyType struct{}

var enforcedSettingsCtxKey = enforcedSettingsCtxKeyType{}

// Clickhouse defines how to connect to a Clickhouse datasource
type Clickhouse struct {
	SchemaDatasource   *schemas.SchemaDatasource
	enforceReadOnly    bool
	enforcedStatic     clickhouse.Settings // static entries + readonly=1; nil when empty
	enforcedBindings   []EnforcedBinding   // all enforced entries (static + header + jwt); nil when empty
	hasDynamicBindings bool                // true iff any binding is non-static; fast path guard
	jwksCache          *jwksCache          // per-instance JWKS cache; non-nil when any jwt verify=jwks binding exists
}

// buildEnforcedStaticChSettings constructs the clickhouse.Settings map that must be
// injected on every query when enforced settings or readonly=1 are configured.
// Returns nil when neither applies.
//
// This map is a per-instance snapshot. resolveEnforcedSettings always copies
// it before handing it out, so the returned reference on this instance is
// never exposed to callers that could mutate it.
//
// Only static-source enforced settings are included; dynamic-source entries
// (e.g. "header") are resolved per-query in resolveEnforcedSettings.
func buildEnforcedStaticChSettings(s Settings) clickhouse.Settings {
	m := s.enforcedSettings()
	if !s.shouldForceReadOnly() && len(m) == 0 {
		return nil
	}
	cs := make(clickhouse.Settings, len(m)+1)
	for k, v := range m {
		cs[k] = v
	}
	if s.shouldForceReadOnly() {
		// clickhouse-go accepts a plain integer here and serialises correctly
		// on both HTTP and Native protocols. Prefer int(1) over uint8(1) so
		// the value type matches other integer-valued settings and cannot
		// surprise a future clickhouse-go release that tightens type checks.
		cs["readonly"] = int(1)
	}
	return cs
}

// enforcedSettingsFromContext returns the enforced clickhouse.Settings stored
// in ctx by MutateQuery. Returns nil when none were attached. Used by tests.
func enforcedSettingsFromContext(ctx context.Context) clickhouse.Settings {
	s, _ := ctx.Value(enforcedSettingsCtxKey).(clickhouse.Settings)
	return s
}

// resolveEnforcedSettings materialises the clickhouse.Settings map for the
// current request. Static bindings are copied from h.enforcedStatic; dynamic
// bindings are resolved from ctx. Returns a downstream error when an
// OnMissing="reject" binding cannot be satisfied.
//
// The returned map is always a fresh allocation; callers may not mutate
// h.enforcedStatic through it.
func (h *Clickhouse) resolveEnforcedSettings(ctx context.Context) (clickhouse.Settings, error) {
	// Fast path: no dynamic bindings. Still allocate a fresh copy so callers
	// (and clickhouse-go's WithSettings) cannot mutate the shared per-instance
	// snapshot.
	if !h.hasDynamicBindings {
		if len(h.enforcedStatic) == 0 {
			return nil, nil
		}
		out := make(clickhouse.Settings, len(h.enforcedStatic))
		for k, v := range h.enforcedStatic {
			out[k] = v
		}
		return out, nil
	}
	out := make(clickhouse.Settings, len(h.enforcedStatic)+len(h.enforcedBindings))
	for k, v := range h.enforcedStatic {
		out[k] = v
	}
	for _, b := range h.enforcedBindings {
		if b.Source.Kind() == customSettingSourceStatic {
			continue
		}
		val, ok, err := b.Source.Resolve(ctx)
		if err != nil {
			return nil, backend.DownstreamError(fmt.Errorf("enforced setting %q: %w", b.Setting, err))
		}
		if !ok {
			if b.OnMissing == onMissingReject {
				backend.Logger.Warn("query rejected: required header-sourced enforced setting absent",
					"setting", b.Setting,
					"source", b.Source.Kind(),
				)
				return nil, backend.DownstreamError(fmt.Errorf(
					"query rejected: required value for enforced setting %q was not present on the request (source=%s)",
					b.Setting, b.Source.Kind(),
				))
			}
			val = ""
		}
		// Defence-in-depth: never let a dynamic binding overwrite readonly.
		if strings.EqualFold(b.Setting, "readonly") {
			continue
		}
		backend.Logger.Debug("resolved enforced setting", "setting", b.Setting, "source", b.Source.Kind())
		out[b.Setting] = wrapCustomSettingValue(b.Setting, val)
	}
	return out, nil
}

// getTLSConfig returns tlsConfig from settings
// logic reused from https://github.com/grafana/grafana/blob/615c153b3a2e4d80cff263e67424af6edb992211/pkg/models/datasource_cache.go#L211
func getTLSConfig(settings Settings) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: settings.InsecureSkipVerify,
		ServerName:         settings.Host,
	}
	if settings.TlsClientAuth || settings.TlsAuthWithCACert {
		if settings.TlsAuthWithCACert && len(settings.TlsCACert) > 0 {
			caPool := x509.NewCertPool()
			if ok := caPool.AppendCertsFromPEM([]byte(settings.TlsCACert)); !ok {
				return nil, backend.DownstreamError(ErrorInvalidCACertificate)
			}
			tlsConfig.RootCAs = caPool
		}
		if settings.TlsClientAuth {
			cert, err := tls.X509KeyPair([]byte(settings.TlsClientCert), []byte(settings.TlsClientKey))
			if err != nil {
				return nil, err
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
	}
	return tlsConfig, nil
}

// getPDCDialContext returns a dialer function for creating a connection to PDC if a secure SOCKS proxy is enabled.
func getPDCDialContext(settings Settings) (func(context.Context, string) (net.Conn, error), error) {
	p := sdkproxy.New(settings.ProxyOptions)

	if !p.SecureSocksProxyEnabled() {
		return nil, nil
	}

	dialer, err := p.NewSecureSocksProxyContextDialer()
	if err != nil {
		return nil, err
	}

	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("unable to cast SOCKS proxy dialer to context proxy dialer")
	}

	return func(ctx context.Context, addr string) (net.Conn, error) {
		return contextDialer.DialContext(ctx, "tcp", addr)
	}, nil
}

func getClientInfoProducts(ctx context.Context) (products []struct{ Name, Version string }) {
	version := backend.UserAgentFromContext(ctx).GrafanaVersion()

	if version != "" {
		products = append(products, struct{ Name, Version string }{
			Name:    "grafana",
			Version: version,
		})
	}

	if info, err := buildinfo.GetBuildInfo(); err == nil {
		products = append(products, struct{ Name, Version string }{
			Name:    "clickhouse-datasource",
			Version: info.Version,
		})
	}

	return products
}

func CheckMinServerVersion(conn *sql.DB, major, minor, patch uint64) (bool, error) {
	var version struct {
		Major uint64
		Minor uint64
		Patch uint64
	}
	var res string
	if err := conn.QueryRow("SELECT version()").Scan(&res); err != nil {
		return false, err
	}
	for i, v := range strings.Split(res, ".") {
		switch i {
		case 0:
			version.Major, _ = strconv.ParseUint(v, 10, 64)
		case 1:
			version.Minor, _ = strconv.ParseUint(v, 10, 64)
		case 2:
			version.Patch, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	if version.Major < major || (version.Major == major && version.Minor < minor) ||
		(version.Major == major && version.Minor == minor && version.Patch < patch) {
		return false, nil
	}
	return true, nil
}

// resolveJWTAuth builds the ClickHouse Auth and GetJWT callback when JWT
// authentication is enabled. The Bearer token is removed from httpHeaders
// (mutated in-place) and returned via the GetJWT callback instead.
func resolveJWTAuth(settings Settings, httpHeaders map[string]string) (clickhouse.Auth, clickhouse.GetJWTFunc) {
	auth := clickhouse.Auth{
		Database: settings.DefaultDatabase,
		Username: settings.Username,
		Password: settings.Password,
	}

	authHeader := httpHeaders[backend.OAuthIdentityTokenHeaderName]
	if !settings.OAuthPassThru || authHeader == "" {
		return auth, nil
	}

	delete(httpHeaders, backend.OAuthIdentityTokenHeaderName)
	token := strings.TrimPrefix(authHeader, "Bearer ")
	return clickhouse.Auth{Database: settings.DefaultDatabase},
		func(context.Context) (string, error) { return token, nil }
}

func wrapCategorizedConnectionError(err error) error {
	category := CategorizeConnectionError(err)
	backend.Logger.Error("failed to create ClickHouse client", "error_category", string(category))
	if category == ConnectionErrorCategoryAuth {
		if hint := authErrorHint(err); hint != "" {
			return backend.DownstreamError(fmt.Errorf("[%s] %w (%s)", category, err, hint))
		}
	}
	return backend.DownstreamError(fmt.Errorf("[%s] %w", category, err))
}

func buildClickHouseOptions(ctx context.Context, settings Settings, message json.RawMessage) (*clickhouse.Options, error) {
	var tlsConfig *tls.Config
	var err error
	if settings.TlsAuthWithCACert || settings.TlsClientAuth {
		tlsConfig, err = getTLSConfig(settings)
		if err != nil {
			return nil, wrapCategorizedConnectionError(err)
		}
	} else if settings.Secure {
		tlsConfig = &tls.Config{
			InsecureSkipVerify: settings.InsecureSkipVerify,
		}
	}

	t, err := strconv.Atoi(settings.DialTimeout)
	if err != nil {
		return nil, backend.DownstreamError(fmt.Errorf("invalid timeout: %s", settings.DialTimeout))
	}
	qt, err := strconv.Atoi(settings.QueryTimeout)
	if err != nil {
		return nil, backend.DownstreamError(fmt.Errorf("invalid query timeout: %s", settings.QueryTimeout))
	}

	protocol := clickhouse.Native
	if settings.Protocol == "http" {
		protocol = clickhouse.HTTP
	}

	compression := clickhouse.CompressionLZ4
	if protocol == clickhouse.HTTP {
		compression = clickhouse.CompressionGZIP
	}

	customSettings := make(clickhouse.Settings)
	if settings.CustomSettings != nil {
		for _, setting := range settings.CustomSettings {
			if !setting.Enforced {
				customSettings[setting.Setting] = setting.Value
			}
		}
	}

	if settings.RowLimit != 0 && settings.EnableRowLimit {
		customSettings["limit"] = settings.RowLimit
	}

	httpHeaders, err := extractForwardedHeadersFromMessage(message)
	if err != nil {
		return nil, err
	}

	// merge settings.HttpHeaders with message httpHeaders
	for k, v := range settings.HttpHeaders {
		httpHeaders[k] = v
	}

	if settings.OAuthPassThru && tlsConfig == nil {
		return nil, backend.DownstreamError(fmt.Errorf("JWT authentication requires a secure (TLS) connection"))
	}

	// Forwarding a real user's token over a connection whose server
	// certificate is not verified exposes the token to interception. This is a
	// higher bar than a shared service credential, so reject the combination.
	if settings.OAuthPassThru && settings.InsecureSkipVerify {
		return nil, backend.DownstreamError(fmt.Errorf("the \"Forward OAuth Identity\" and \"Skip TLS Verify\" options cannot be combined: forwarding a user token over an unverified TLS connection would expose it to interception"))
	}

	// When Forward OAuth Identity is enabled, a data query (message != nil)
	// that arrives without a forwarded user token is a backend query with no
	// user to attribute it to — typically alert rule evaluation. Health checks
	// and schema introspection pass a nil message and always fall back, since
	// no user token is ever available for them.
	if settings.OAuthPassThru && message != nil && httpHeaders[backend.OAuthIdentityTokenHeaderName] == "" {
		if !settings.OAuthPassThruAllowFallback {
			return nil, backend.DownstreamError(fmt.Errorf(
				"this query carries no user identity but \"Forward OAuth Identity\" is enabled; " +
					"it is running outside a user session (for example, an alert rule). Enable " +
					"\"Allow service account fallback\" on the data source to let these queries " +
					"run with the configured username/password, or ensure the request forwards a user OAuth token"))
		}
		// Fallback is opt-in and exercised: warn so the privilege divergence is
		// not silent. These queries run as the shared service account and are
		// not subject to the per-user row policies or quotas that OAuth
		// pass-through enforces for interactive queries.
		backend.Logger.Warn("Forward OAuth Identity: query has no forwarded user identity; " +
			"falling back to the configured username/password (service account)")
	}

	auth, getJWT := resolveJWTAuth(settings, httpHeaders)

	opts := &clickhouse.Options{
		Addr: []string{fmt.Sprintf("%s:%d", settings.Host, settings.Port)},
		Auth: auth,
		ClientInfo: clickhouse.ClientInfo{
			Products: getClientInfoProducts(ctx),
		},
		Compression: &clickhouse.Compression{
			Method: compression,
		},
		DialTimeout: time.Duration(t) * time.Second,
		GetJWT:      getJWT,
		HttpHeaders: httpHeaders,
		HttpUrlPath: settings.Path,
		Protocol:    protocol,
		ReadTimeout: time.Duration(qt) * time.Second,
		Settings:    customSettings,
		TLS:         tlsConfig,
	}

	// dialCtx is used to create a connection to PDC, if it is enabled above
	dialCtx, err := getPDCDialContext(settings)
	if err != nil {
		return nil, err
	}
	if dialCtx != nil {
		opts.DialContext = dialCtx
	}

	return opts, nil
}

// Connect opens a sql.DB connection using datasource settings
func (h *Clickhouse) Connect(
	ctx context.Context,
	config backend.DataSourceInstanceSettings,
	message json.RawMessage,
) (*sql.DB, error) {
	ctx, span := tracing.DefaultTracer().Start(ctx, "clickhouse connect", trace.WithAttributes(
		attribute.String("db.system", "clickhouse"),
	))

	defer span.End()

	settings, err := LoadSettings(ctx, config)
	if err != nil {
		return nil, wrapCategorizedConnectionError(err)
	}

	opts, err := buildClickHouseOptions(ctx, settings, message)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, opts.DialTimeout)
	defer cancel()

	db := clickhouse.OpenDB(opts)

	// Set connection pool settings
	if i, err := strconv.Atoi(settings.ConnMaxLifetime); err == nil {
		db.SetConnMaxLifetime(time.Duration(i) * time.Minute)
	}
	if i, err := strconv.Atoi(settings.MaxIdleConns); err == nil {
		db.SetMaxIdleConns(i)
	}
	if i, err := strconv.Atoi(settings.MaxOpenConns); err == nil {
		db.SetMaxOpenConns(i)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("the operation was cancelled before starting: %w", ctx.Err())
	default:
		// proceed
	}

	// `sqlds` normally calls `db.PingContext()` to check if the connection is alive,
	// however, as ClickHouse returns its own non-standard `Exception` type, we need
	// to handle it here so that we can categorize and surface the error correctly.
	if err := db.PingContext(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("the operation was cancelled during execution: %w", ctx.Err())
		}

		return nil, wrapCategorizedConnectionError(err)
	}

	// Honor the (nil-resource-on-error) contract so callers can rely on
	// `if err != nil { return err }` without leaking the *sql.DB.
	if err := settings.isValid(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Converters defines list of data type converters
func (h *Clickhouse) Converters() []sqlutil.Converter {
	return converters.ClickhouseConverters
}

// Macros returns an empty macro map. ClickHouse macros are expanded by the
// macropro-backed sqlds.Interpolator installed in NewDatasource, which
// replaces sqlds's sqlutil.Interpolate pipeline entirely, so this map is
// never consulted on the query path. macropro.DefaultMacros is already
// merged into macros.ClickHouseMacros, so nothing is lost by leaving it
// empty.
func (h *Clickhouse) Macros() sqlds.Macros {
	return sqlds.Macros{}
}

// MutateQueryError marks ClickHouse errors as downstream errors.
// When EnforceReadOnly is active, READONLY errors (code 164) are annotated
// with a hint explaining that this datasource injects readonly=1 on every
// query — the original ClickHouse error is preserved verbatim (both in the
// message and in the error chain) so operators can distinguish an
// enforcement collision from an unrelated readonly failure.
func (h *Clickhouse) MutateQueryError(err error) backend.ErrorWithSource {
	var ex *clickhouse.Exception
	if errors.As(err, &ex) && ex.Code == 164 && h.enforceReadOnly {
		// Preserve the original ClickHouse error via %w so error unwrapping
		// (errors.As, errors.Is) continues to work. Frame the enforcement
		// context as a hint rather than as the primary message, so that a
		// user reading the error sees the real cause first.
		hint := "hint: this datasource injects readonly=1 on every query; SET or SETTINGS clauses that change server settings will be rejected. If your query does not set any server settings, the target setting may be marked non-CHANGEABLE_IN_READONLY on the server."
		wrapped := fmt.Errorf("%w (%s)", err, hint)
		return backend.NewErrorWithSource(wrapped, backend.ErrorSourceDownstream)
	}
	// Check if any error in the error chain (including multi-errors) is a clickhouse.Exception
	if containsClickHouseException(err) {
		return backend.NewErrorWithSource(err, backend.ErrorSourceDownstream)
	}
	return backend.NewErrorWithSource(err, backend.DefaultErrorSource)
}

// containsClickHouseException checks if err or any error in its chain is a clickhouse.Exception
// It also handles errors wrapped in HTTP response bodies
func containsClickHouseException(err error) bool {
	if err == nil {
		return false
	}

	// Check if the current error is directly a clickhouse.Exception
	var wrappedException *clickhouse.Exception
	if errors.As(err, &wrappedException) {
		return true
	}

	errStr := err.Error()

	// Look for common ClickHouse error patterns in response bodies
	if strings.Contains(errStr, "DB::Exception") {
		return true
	}

	// Catch legacy ClickHouse HTTP error format.
	// This is more general than the above and we attempt the DB::Exception catch first
	// as those errors also contain this pattern.
	// We're only catching 4xx errors for now but we can expand to 5xx if needed.
	matcher, _ := regexp.Compile(`(\[HTTP 4\d\d\])`)
	if matcher.MatchString(errStr) {
		return true
	}

	// Check for multiple wrapped errors (e.g., from errors.Join)
	type multiError interface {
		Unwrap() []error
	}

	if u, ok := err.(multiError); ok {
		for _, e := range u.Unwrap() {
			if containsClickHouseException(e) {
				return true
			}
		}
	}

	return false
}

func (h *Clickhouse) Settings(ctx context.Context, config backend.DataSourceInstanceSettings) sqlds.DriverSettings {
	settings, err := LoadSettings(ctx, config)
	timeout := 60
	if err == nil {
		t, err := strconv.Atoi(settings.QueryTimeout)
		if err == nil {
			timeout = t
		}
	}
	return sqlds.DriverSettings{
		Timeout: time.Second * time.Duration(timeout),
		FillMode: &data.FillMissing{
			Mode: data.FillModeNull,
		},
		ForwardHeaders:  settings.ForwardGrafanaHeaders || settings.OAuthPassThru,
		RowCapacityHint: settings.RowCapacityHint,
	}
}

// MutateQueryData extracts Grafana contextual headers from the request and
// stores them in the context for ClickHouse query metadata injection. It also
// extracts forwarded HTTP headers, resolves enforced-settings bindings once per
// request, and stores the result (or error) in ctx for MutateQuery / interpolateMacros.
func (h *Clickhouse) MutateQueryData(
	ctx context.Context,
	req *backend.QueryDataRequest,
) (context.Context, *backend.QueryDataRequest) {
	httpHeaders := req.GetHTTPHeaders()
	gh := grafanaHeaders{
		DashboardUID: httpHeaders.Get("X-Dashboard-Uid"),
		PanelID:      httpHeaders.Get("X-Panel-Id"),
		RuleUID:      httpHeaders.Get("X-Rule-Uid"),
	}
	if gh.DashboardUID != "" || gh.PanelID != "" || gh.RuleUID != "" {
		ctx = context.WithValue(ctx, grafanaHeadersKey, gh)
	}

	injectGrafanaUserHeader(ctx, req)

	// Build the canonicalised forwarded-headers map from two sources:
	//   1. The HTTP-level req.GetHTTPHeaders() map. These headers have highest
	//      precedence because they are supplied by Grafana/proxy request handling.
	//   2. The query JSON body (sqlds's grafana-http-headers key) — used when
	//      ForwardHeaders is on and sqlds serialises them into ConnectionArgs.
	fwdRaw, _ := extractForwardedHeadersFromMessage(firstQueryMessage(req))
	canon := make(map[string]string, len(fwdRaw)+len(httpHeaders))
	for k, vv := range httpHeaders {
		ck := http.CanonicalHeaderKey(k)
		if len(vv) == 0 {
			continue
		}
		if len(vv) > 1 {
			backend.Logger.Warn("dropping multi-valued forwarded header; configure your proxy to send a single value",
				"header", ck, "value_count", len(vv))
			continue
		}
		canon[ck] = vv[0]
	}
	for k, v := range fwdRaw {
		ck := http.CanonicalHeaderKey(k)
		if existing, exists := canon[ck]; exists {
			if existing != v {
				backend.Logger.Warn("dropping query-body header override; HTTP header takes precedence",
					"header", ck, "http_len", len(existing), "body_len", len(v))
			}
			continue
		}
		canon[ck] = v
	}
	ctx = WithForwardedHeaders(ctx, canon)

	if len(h.enforcedBindings) > 0 {
		resolved, resolveErr := h.resolveEnforcedSettings(ctx)
		if resolveErr != nil {
			ctx = context.WithValue(ctx, resolvedEnforcedErrCtxKey, resolveErr)
		} else {
			ctx = context.WithValue(ctx, resolvedEnforcedCtxKey, resolved)
		}
	}

	req = preprocessGrafanaSQL(req)
	return ctx, req
}

// firstQueryMessage returns the JSON of the first query in req that has
// non-empty JSON. This is what sqlds forwards to Connect as the connection
// message and where forwarded HTTP headers are embedded.
func firstQueryMessage(req *backend.QueryDataRequest) json.RawMessage {
	if req == nil {
		return nil
	}
	for _, q := range req.Queries {
		if len(q.JSON) > 0 {
			return q.JSON
		}
	}
	return nil
}

// injectGrafanaUserHeader populates X-Grafana-User from the request's user
// context when "Forward Grafana HTTP Headers" is enabled. Grafana's
// `dataproxy.send_user_header` setting only adds the header to the proxy
// path (core HTTP datasources); plugin-initiated connections never see it,
// so downstream ClickHouse loses the user attribution that operators expect
// when the toggle is on. See #1451.
func injectGrafanaUserHeader(ctx context.Context, req *backend.QueryDataRequest) {
	if req == nil || req.PluginContext.DataSourceInstanceSettings == nil {
		return
	}
	if req.GetHTTPHeader("X-Grafana-User") != "" {
		return
	}
	settings, err := LoadSettings(ctx, *req.PluginContext.DataSourceInstanceSettings)
	if err != nil || !settings.ForwardGrafanaHeaders {
		return
	}
	user := backend.UserFromContext(ctx)
	if user == nil || user.Login == "" {
		return
	}
	req.SetHTTPHeader("X-Grafana-User", user.Login)
}

// interpolateMacros is the sqlds.Interpolator installed by NewDatasource. It
// replaces sqlds's default sqlutil.Interpolate pipeline with macropro, so
// macropro owns macro parsing end-to-end and handlers receive the fully
// parsed query — including Table and Column, which the previous
// MutateQueryData pre-expansion never carried. Expansion errors return
// straight to the query response rather than being smuggled through a
// throwIf() rewrite that failed at execution time.
//
// Every error is wrapped as a downstream error: interpolation failures
// originate from the user's query text (bad macro arguments, missing
// table/column context, parse errors), never from a plugin bug. Our own
// handlers already wrap backend.DownstreamError, but macropro's default
// handlers ($__table, $__column) return plain errors, and sqlds only
// downstream-classifies bad-argument-count and bracket errors on its own —
// so without this wrap those would be miscounted as plugin errors.
//
// Before interpolating, the function checks for a binding-resolution error
// stored in ctx by MutateQueryData; if present the query is immediately
// rejected without touching the database.
func interpolateMacros(ctx context.Context, query *sqlutil.Query, _ json.RawMessage) (string, error) {
	if err, ok := ctx.Value(resolvedEnforcedErrCtxKey).(error); ok {
		return "", err // already wrapped in backend.DownstreamError by resolveEnforcedSettings
	}
	sql, err := macros.Interpolate(query.RawSQL, query)
	if err != nil {
		return "", backend.DownstreamError(err)
	}
	return sql, nil
}

func preprocessGrafanaSQL(req *backend.QueryDataRequest) *backend.QueryDataRequest {
	if req == nil || len(req.Queries) == 0 {
		return req
	}

	queries := make([]backend.DataQuery, 0, len(req.Queries))
	for _, q := range req.Queries {
		var sq schemas.Query

		if err := json.Unmarshal(q.JSON, &sq); err != nil {
			// Cannot unmarshal query JSON, ignoring
			queries = append(queries, q)
			continue
		}

		if !sq.GrafanaSql {
			// Not a Grafana SQL query, ignoring
			queries = append(queries, q)
			continue
		}

		sqlQuery, err := sq.ToSQL(schemas.DialectClickHouse)
		if err != nil {
			backend.Logger.Error("Failed to build SQL query", "error", err.Error())
			continue
		}

		// Build JSON with `sqlutil.Query` shape that will be used to execute the query by sqlds
		queryJSON, err := json.Marshal(sqlutil.Query{
			RawSQL:         sqlQuery,
			Format:         sqlutil.FormatOptionTable, // TODO: Is this correct?
			ConnectionArgs: json.RawMessage("{}"),
		})
		if err != nil {
			backend.Logger.Error("Failed to marshal SQL query", "error", err.Error())
			continue
		}

		q.JSON = queryJSON
		queries = append(queries, q)
	}

	return &backend.QueryDataRequest{
		PluginContext: req.PluginContext,
		Headers:       req.Headers,
		Queries:       queries,
	}
}

func (h *Clickhouse) MutateQuery(ctx context.Context, req backend.DataQuery) (context.Context, backend.DataQuery) {
	ctx, span := tracing.DefaultTracer().Start(ctx, "clickhouse mutate_query", trace.WithAttributes(
		attribute.String("db.system", "clickhouse"),
	))

	defer span.End()

	// If MutateQueryData already stored a binding-resolution error in ctx
	// (e.g. required header/JWT absent, JWKS verification failed), skip the
	// rest of the pipeline: interpolateMacros will surface that error and the
	// query never reaches the database. Doing extra work here (span
	// attributes, query-comment building, timezone parsing) is wasted and
	// would only pollute traces for a query that's about to be rejected.
	if _, hasErr := ctx.Value(resolvedEnforcedErrCtxKey).(error); hasErr {
		span.SetAttributes(attribute.Bool("clickhouse.enforced_settings.rejected", true))
		return ctx, req
	}

	// Prefer the per-request resolved settings (attached in MutateQueryData).
	// Falls back to h.enforcedStatic when MutateQueryData did not run — unit
	// tests instantiate Clickhouse directly without going through the sqlds
	// query-data pipeline.
	var resolved clickhouse.Settings
	if v, ok := ctx.Value(resolvedEnforcedCtxKey).(clickhouse.Settings); ok {
		resolved = v
	} else if !h.hasDynamicBindings {
		resolved = h.enforcedStatic
	}

	if len(resolved) > 0 {
		ctx = clickhouse.Context(ctx, clickhouse.WithSettings(resolved))
		ctx = context.WithValue(ctx, enforcedSettingsCtxKey, resolved)

		// Record the number of user-configured enforced settings (not counting
		// readonly=1 itself) and whether read-only enforcement is active.
		// Values are intentionally omitted — they can encode tenant identity.
		userSettingCount := len(resolved)
		if h.enforceReadOnly {
			userSettingCount--
		}
		headerCount := 0
		headerResolved := 0
		jwtCount := 0
		jwtResolved := 0
		for _, b := range h.enforcedBindings {
			switch b.Source.Kind() {
			case customSettingSourceHeader:
				headerCount++
				if _, ok := resolved[b.Setting]; ok {
					headerResolved++
				}
			case CustomSettingSourceJWT:
				jwtCount++
				if _, ok := resolved[b.Setting]; ok {
					jwtResolved++
				}
			}
		}
		span.SetAttributes(
			attribute.Int("clickhouse.enforced_settings.count", userSettingCount),
			attribute.Bool("clickhouse.enforced_readonly", h.enforceReadOnly),
			attribute.Int("clickhouse.enforced_settings.header_sourced.count", headerCount),
			attribute.Int("clickhouse.enforced_settings.header_sourced.resolved", headerResolved),
			attribute.Int("clickhouse.enforced_settings.jwt_sourced.count", jwtCount),
			attribute.Int("clickhouse.enforced_settings.jwt_sourced.resolved", jwtResolved),
		)
	}

	comments := make([]string, 0, 4+len(h.enforcedBindings))

	if user := backend.UserFromContext(ctx); user != nil {
		comments = append(comments, "grafana_user:"+user.Login)
	}

	if gh, ok := ctx.Value(grafanaHeadersKey).(grafanaHeaders); ok {
		if gh.DashboardUID != "" {
			comments = append(comments, "grafana_dashboard:"+gh.DashboardUID)
		}
		if gh.PanelID != "" {
			comments = append(comments, "grafana_panel:"+gh.PanelID)
		}
		if gh.RuleUID != "" {
			comments = append(comments, "grafana_rule:"+gh.RuleUID)
		}
	}

	// Tag each dynamically-sourced enforced setting whose value was resolved for
	// this request so operators can correlate query comments to binding config.
	// Setting names only — never values.
	for _, b := range h.enforcedBindings {
		switch b.Source.Kind() {
		case customSettingSourceHeader:
			if resolved != nil {
				if _, ok := resolved[b.Setting]; ok {
					comments = append(comments, "enforced_from_header:"+b.Setting)
				}
			}
		case CustomSettingSourceJWT:
			if resolved != nil {
				if _, ok := resolved[b.Setting]; ok {
					comments = append(comments, "enforced_from_jwt:"+b.Setting)
				}
			}
		}
	}

	if len(comments) > 0 {
		ctx = clickhouse.Context(ctx, clickhouse.WithClientInfo(clickhouse.ClientInfo{
			Products: nil,
			Comment:  comments,
		}))
	}

	var dataQuery struct {
		Meta struct {
			TimeZone string `json:"timezone"`
		} `json:"meta"`
		Format int `json:"format"`
	}

	if err := json.Unmarshal(req.JSON, &dataQuery); err != nil {
		return ctx, req
	}

	if dataQuery.Meta.TimeZone == "" {
		return ctx, req
	}

	loc, _ := time.LoadLocation(dataQuery.Meta.TimeZone)
	return clickhouse.Context(ctx, clickhouse.WithUserLocation(loc)), req
}

// MutateResponse converts fields of type FieldTypeNullableJSON to string,
// except for specific visualizations (traces, tables, and logs).
func (h *Clickhouse) MutateResponse(ctx context.Context, res data.Frames) (data.Frames, error) {
	_, span := tracing.DefaultTracer().Start(ctx, "clickhouse mutate_response", trace.WithAttributes(
		attribute.String("db.system", "clickhouse"),
	))

	defer span.End()

	for _, frame := range res {
		if frame.Meta.PreferredVisualization == data.VisTypeLogs {
			err := mergeOpenTelemetryLabels(frame)
			if err != nil {
				return nil, err
			}
		}

		if shouldConvertFields(frame.Meta.PreferredVisualization) {
			if err := convertNullableJSONFields(frame); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// shouldConvertFields determines whether field conversion is needed based on visualization type.
func shouldConvertFields(visType data.VisType) bool {
	return visType != data.VisTypeTrace && visType != data.VisTypeTable && visType != data.VisTypeLogs
}

// convertNullableJSONFields converts all FieldTypeNullableJSON fields in the given frame to string.
func convertNullableJSONFields(frame *data.Frame) error {
	var convertedFields []*data.Field

	for _, field := range frame.Fields {
		if field.Type() == data.FieldTypeJSON {
			newField, err := convertFieldToString(field)
			if err != nil {
				return err
			}
			convertedFields = append(convertedFields, newField)
		} else {
			convertedFields = append(convertedFields, field)
		}
	}

	frame.Fields = convertedFields
	return nil
}

// convertFieldToString creates a new field where JSON values are marshaled into string representations.
func convertFieldToString(field *data.Field) (*data.Field, error) {
	values := make([]*string, field.Len())
	newField := data.NewField(field.Name, field.Labels, values)
	newField.SetConfig(field.Config)

	for i := 0; i < field.Len(); i++ {
		val, _ := field.At(i).(*json.RawMessage)
		if val == nil {
			newField.Set(i, nil)
		} else {
			bytes, err := val.MarshalJSON()
			if err != nil {
				return nil, err
			}
			sVal := string(bytes)
			newField.Set(i, &sVal)
		}
	}

	return newField, nil
}

func extractForwardedHeadersFromMessage(message json.RawMessage) (map[string]string, error) {
	// An example of the message we're trying to parse:
	// {
	//   "grafana-http-headers": {
	//     "x-grafana-org-id": ["12345"],
	//     "x-grafana-user": ["admin"]
	//   }
	// }
	if len(message) == 0 {
		message = []byte("{}")
	}

	messageArgs := make(map[string]interface{})
	err := json.Unmarshal(message, &messageArgs)
	if err != nil {
		backend.Logger.Warn(fmt.Sprintf("Failed to apply headers: %s", err.Error()))
		return nil, errors.New("couldn't parse message as args")
	}

	httpHeaders := make(map[string]string)
	if grafanaHttpHeaders, ok := messageArgs[sqlds.HeaderKey]; ok {
		fwdHeaders, ok := grafanaHttpHeaders.(map[string]interface{})
		if !ok {
			return nil, errors.New("couldn't parse grafana HTTP headers")
		}

		for k, v := range fwdHeaders {
			anyHeadersArr, ok := v.([]interface{})
			if !ok {
				return nil, fmt.Errorf("couldn't parse header %s as an array", k)
			}

			if len(anyHeadersArr) == 0 {
				continue
			}

			// Check for multi-valued headers and reject them
			if len(anyHeadersArr) > 1 {
				ck := http.CanonicalHeaderKey(k)
				backend.Logger.Warn("dropping multi-valued forwarded header; configure your proxy to send a single value",
					"header", ck, "value_count", len(anyHeadersArr))
				continue
			}

			// Validate the single element is a string
			val, ok := anyHeadersArr[0].(string)
			if !ok {
				return nil, fmt.Errorf("couldn't parse header %s: element 0 is not a string", k)
			}

			httpHeaders[k] = val
		}
	}

	return httpHeaders, nil
}

func mergeOpenTelemetryLabels(frame *data.Frame) error {
	var attrFields []*data.Field
	for _, field := range frame.Fields {
		if field.Name == "labels" {
			return nil
		}

		if field.Type() != data.FieldTypeJSON {
			continue
		}

		if field.Name == "ResourceAttributes" || field.Name == "ScopeAttributes" || field.Name == "LogAttributes" {
			attrFields = append(attrFields, field)
		}
	}

	if len(attrFields) == 0 {
		return nil
	}

	rowLen, err := frame.RowLen()
	if err != nil {
		return err
	}

	allLabelsValues := make([]map[string]any, rowLen)

	for _, field := range attrFields {
		for j := 0; j < rowLen; j++ {
			currentVal := allLabelsValues[j]
			if currentVal == nil {
				currentVal = make(map[string]any)
			}

			val := field.At(j).(json.RawMessage)
			if val != nil {
				var valMap map[string]any
				err := json.Unmarshal(val, &valMap)
				if err != nil {
					return err
				}

				assignFlattenedPath(currentVal, field.Name, "", valMap)

				allLabelsValues[j] = currentVal
			}
		}
	}

	allLabelsValuesJSON := make([]json.RawMessage, rowLen)
	for i, value := range allLabelsValues {
		valueJSON, err := json.Marshal(value)
		if err != nil {
			return err
		}

		allLabelsValuesJSON[i] = valueJSON
	}
	allLabels := data.NewField("labels", make(data.Labels), allLabelsValuesJSON)

	filteredFields := make([]*data.Field, 0, len(frame.Fields)-len(attrFields))
	for _, field := range frame.Fields {
		if field.Name == "ResourceAttributes" || field.Name == "ScopeAttributes" || field.Name == "LogAttributes" {
			continue
		}

		filteredFields = append(filteredFields, field)
	}
	filteredFields = append(filteredFields, allLabels)
	frame.Fields = filteredFields

	return nil
}

// assignFlattenedPath will flatten a nested map into a map with top level keys separated by dots.
func assignFlattenedPath(flatMap map[string]any, pathPrefix, pathKey string, pathValue any) {
	fullPath := fmt.Sprintf("%s.%s", pathPrefix, pathKey)
	if pathKey == "" {
		fullPath = pathPrefix
	}

	nestedMap, ok := pathValue.(map[string]any)
	if !ok {
		flatMap[fullPath] = pathValue
		return
	}

	for k, v := range nestedMap {
		assignFlattenedPath(flatMap, fullPath, k, v)
	}
}
