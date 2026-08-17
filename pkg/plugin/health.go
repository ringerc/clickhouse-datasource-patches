package plugin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	keyfunc "github.com/MicahParks/keyfunc/v3"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// dbProber abstracts the two DB operations used by the enforced-settings health probes,
// allowing the probe logic to be unit-tested without a real ClickHouse connection.
type dbProber struct {
	// queryScalar executes query and scans the first column of the first row as a string.
	queryScalar func(ctx context.Context, query string) (string, error)
	// execQuery executes query and returns any error (nil = success).
	execQuery func(ctx context.Context, query string) error
}

// sqlDBProber builds a dbProber backed by db.
// Callers supply a context already enriched with clickhouse.Context settings when needed.
func sqlDBProber(db *sql.DB) dbProber {
	return dbProber{
		queryScalar: func(ctx context.Context, query string) (string, error) {
			var s string
			return s, db.QueryRowContext(ctx, query).Scan(&s)
		},
		execQuery: func(ctx context.Context, query string) error {
			rows, err := db.QueryContext(ctx, query)
			if rows != nil {
				rows.Close()
			}
			return err
		},
	}
}

// enforcedProbeTimeout returns the timeout to use for each individual probe.
// Capped at 30 s so health checks stay responsive.
func enforcedProbeTimeout(s Settings) time.Duration {
	qt, err := strconv.Atoi(s.QueryTimeout)
	if err != nil || qt <= 0 {
		qt = 30
	}
	if qt > 30 {
		qt = 30
	}
	return time.Duration(qt) * time.Second
}

// runEnforcedHealthProbes runs the three enforced-settings health probes and returns
// a non-nil StatusError result on the first failure, or nil when all pass.
//
// For static-source bindings, the three probes are:
//
//	(c) Startup readonly check — the connecting user must start at readonly=0 so the plugin
//	    can inject the enforced settings on each query.
//	(a) Round-trip — getSetting('<name>') under the enforced settings map must return the
//	    configured value.
//	(b) Override-rejection — an inline SETTINGS override of each enforced name must fail
//	    with ClickHouse error 164 (READONLY). Success means the setting is marked
//	    CHANGEABLE_IN_READONLY, which silently breaks the guarantee.
//
// For header-source bindings, probes (a) and (b) are skipped because the value
// is not known at save time. A nil result is returned for those entries; the
// caller (makeEnforcedSettingsHealthCheck) appends an info-level note.
//
// The function is package-level (not a method) so it can be unit-tested with a fake prober.
func runEnforcedHealthProbes(ctx context.Context, s Settings, p dbProber) *backend.CheckHealthResult {
	if !s.shouldForceReadOnly() {
		return nil
	}

	bindings, err := s.enforcedBindings()
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: fmt.Sprintf("Enforced-settings configuration error: %s", err),
		}
	}

	// Check whether any static binding exists; if not, skip the DB probes entirely.
	hasStatic := false
	for _, b := range bindings {
		if b.Source.Kind() == customSettingSourceStatic {
			hasStatic = true
			break
		}
	}

	timeout := enforcedProbeTimeout(s)

	// Probe (c): verify the connecting user starts at readonly=0.
	// Only needed when there are static bindings that require DB round-trips.
	if hasStatic {
		cCtx, cCancel := context.WithTimeout(ctx, timeout)
		defer cCancel()
		roVal, err := p.queryScalar(cCtx, "SELECT value FROM system.settings WHERE name='readonly'")
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				backend.Logger.Warn("enforced-settings health: could not query system.settings for readonly", "error", err)
			}
			// Best-effort check; the basic connectivity probe already passed.
		} else if roVal != "0" {
			return &backend.CheckHealthResult{
				Status: backend.HealthStatusError,
				Message: fmt.Sprintf(
					"Connecting ClickHouse user is already readonly=%s; enforced settings cannot be applied on top. Use a user that starts at readonly=0.",
					roVal,
				),
			}
		}
	}

	// Build a context that carries the enforced static settings for probes (a) and (b).
	enforcedCtx := clickhouse.Context(ctx, clickhouse.WithSettings(buildEnforcedStaticChSettings(s)))

	for _, b := range bindings {
		if b.Source.Kind() != customSettingSourceStatic {
			// Header-sourced (and other dynamic) bindings are not validated at save
			// time — the value is only known at request time.
			continue
		}

		// Unwrap the configured value for comparison with getSetting output.
		var cfgValue string
		staticMap := s.enforcedSettings()
		if cfgValueIface, ok := staticMap[b.Setting]; ok {
			if cs, ok := cfgValueIface.(clickhouse.CustomSetting); ok {
				cfgValue = cs.Value
			} else {
				cfgValue = fmt.Sprint(cfgValueIface)
			}
		}

		// Probe (a): round-trip — getSetting must return the configured value.
		aCtx, aCancel := context.WithTimeout(enforcedCtx, timeout)
		got, err := p.queryScalar(aCtx, fmt.Sprintf("SELECT getSetting('%s')", b.Setting))
		aCancel()
		if err != nil {
			return &backend.CheckHealthResult{
				Status: backend.HealthStatusError,
				Message: fmt.Sprintf(
					"Enforced setting %q: health probe failed to read back the value via getSetting: %s",
					b.Setting, err,
				),
			}
		}
		if got != cfgValue {
			return &backend.CheckHealthResult{
				Status: backend.HealthStatusError,
				Message: fmt.Sprintf(
					"Enforced setting %q: value mismatch — sent %q but getSetting returned %q. "+
						"Check your ClickHouse settings-constraints profile: if the setting is CONST with a different value, the enforced value is silently ignored.",
					b.Setting, cfgValue, got,
				),
			}
		}

		// Probe (b): override-rejection — an inline SETTINGS override must fail with code 164.
		bCtx, bCancel := context.WithTimeout(enforcedCtx, timeout)
		execErr := p.execQuery(bCtx, fmt.Sprintf("SELECT 1 SETTINGS %s = '__grafana_enforced_probe__'", b.Setting))
		bCancel()
		if execErr == nil {
			// Override succeeded: the setting is CHANGEABLE_IN_READONLY, breaking the guarantee.
			return &backend.CheckHealthResult{
				Status: backend.HealthStatusError,
				Message: fmt.Sprintf(
					"Server permits per-query override of enforced setting %q. "+
						"Check your ClickHouse settings-constraints profile: the setting must not be marked CHANGEABLE_IN_READONLY.",
					b.Setting,
				),
			}
		}
		// Code 164 (READONLY) is the expected outcome. Any other error is ambiguous —
		// log a warning but do not fail the health check.
		var ex *clickhouse.Exception
		if !errors.As(execErr, &ex) || ex.Code != 164 {
			backend.Logger.Warn("enforced-settings health: override probe returned unexpected error",
				"setting", b.Setting,
			)
		}
	}

	return nil
}

// makeEnforcedSettingsHealthCheck returns a sqlds-compatible PostCheckHealth function
// that opens a short-lived probe connection and runs runEnforcedHealthProbes.
func makeEnforcedSettingsHealthCheck(s Settings, instanceSettings backend.DataSourceInstanceSettings) func(context.Context, *backend.CheckHealthRequest) *backend.CheckHealthResult {
	return func(ctx context.Context, req *backend.CheckHealthRequest) *backend.CheckHealthResult {
		// Gate: if any dynamic-source binding depends on a header that is
		// not always forwarded, the datasource "Forward Grafana HTTP
		// Headers" toggle must be on. Otherwise the header will never
		// reach the plugin and every request will fall through to
		// OnMissing (either silently blanking the setting or hard-failing
		// the query). Surface this as a health-check error so operators
		// notice it before end users hit it.
		if result := checkForwardHeadersGate(s); result != nil {
			return result
		}

		// Use a fresh connection so the probe is independent of the pooled connection
		// and does not interfere with in-flight queries.
		plugin := Clickhouse{}
		db, err := plugin.Connect(ctx, instanceSettings, nil)
		if err != nil {
			backend.Logger.Warn("enforced-settings health: could not open probe connection", "error", err)
			return nil
		}
		defer db.Close()

		result := runEnforcedHealthProbes(ctx, s, sqlDBProber(db))
		if result != nil {
			return result
		}

		// Append informational notes when any dynamic-source bindings exist.
		bindings, bErr := s.enforcedBindings()
		if bErr != nil {
			return nil
		}

		var infoLines []string

		for _, b := range bindings {
			switch b.Source.Kind() {
			case customSettingSourceHeader:
				headerName := ""
				if hn, ok := b.Source.(interface{ HeaderName() string }); ok {
					headerName = hn.HeaderName()
				}
				if headerName != "" {
					infoLines = append(infoLines, fmt.Sprintf(
						"header-sourced value for %q (from header %q) is not validated at save time; verify at runtime",
						b.Setting, headerName,
					))
				} else {
					infoLines = append(infoLines, fmt.Sprintf(
						"header-sourced value for %q is not validated at save time; verify at runtime",
						b.Setting,
					))
				}

			case CustomSettingSourceJWT:
				type jwtProbe interface {
					JWKSURL() string
					Verify() string
					ClaimPath() string
					HeaderName() string
				}
				jp, ok := b.Source.(jwtProbe)
				if !ok {
					continue
				}
				if jp.Verify() == CustomSettingJWTVerifyJWKS {
					jwksURL := jp.JWKSURL()
					probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
					probeErr := probeJWKSURL(probeCtx, jwksURL)
					cancel()
					if probeErr != nil {
						return &backend.CheckHealthResult{
							Status: backend.HealthStatusError,
							Message: fmt.Sprintf(
								"JWKS URL for setting %q (%q) is unreachable: %s",
								b.Setting, jwksURL, probeErr,
							),
						}
					}
					infoLines = append(infoLines, fmt.Sprintf(
						"JWT-sourced value for %q (claim %q from header %q, verify=jwks): JWKS URL %q is reachable",
						b.Setting, jp.ClaimPath(), jp.HeaderName(), jwksURL,
					))
				} else {
					infoLines = append(infoLines, fmt.Sprintf(
						"JWT-sourced value for %q (claim %q from header %q) is not validated at save time; ensure the token is forwarded",
						b.Setting, jp.ClaimPath(), jp.HeaderName(),
					))
				}
			}
		}

		if len(infoLines) == 0 {
			return nil
		}
		msg := "This datasource resolves one or more enforced settings from request headers or JWT claims; " +
			"ensure Grafana is configured to forward the header(s)/token(s) to backend plugins.\n" +
			strings.Join(infoLines, "\n")
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusOk,
			Message: msg,
		}
	}
}

// probeJWKSURL performs an end-to-end JWKS fetch: it uses the same keyfunc
// library the runtime uses to verify tokens and reports an error if either
// the HTTP fetch or the JWK Set parse fails. This is stronger than a raw
// GET because it exercises the exact code path (headers, decoding, key
// requirement checks) that a real query would trigger — a JWKS URL that
// serves the wrong content type, empty JSON, or an invalid key set will
// fail here even though a plain GET would return HTTP 200.
//
// The context timeout gates both the fetch and the parse; on success the
// underlying background refresh goroutine is immediately cancelled so the
// probe leaves no goroutines behind.
func probeJWKSURL(ctx context.Context, url string) error {
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel() // terminates the keyfunc refresh goroutine on return

	client := &http.Client{Timeout: jwksFetchTimeout}
	noErrFirst := false
	kf, err := keyfunc.NewDefaultOverrideCtx(probeCtx, []string{url}, keyfunc.Override{
		Client:                    client,
		HTTPTimeout:               jwksFetchTimeout,
		RefreshInterval:           jwksRefreshInterval,
		NoErrorReturnFirstHTTPReq: &noErrFirst,
	})
	if err != nil {
		return err
	}
	// Sanity check: the key set must contain at least one key. An empty
	// JWKS ({"keys":[]}) parses without error but would fail every
	// verification at query time.
	keys, err := kf.Storage().KeyReadAll(probeCtx)
	if err != nil {
		return fmt.Errorf("read JWKS keys: %w", err)
	}
	if len(keys) == 0 {
		return fmt.Errorf("JWKS document contains no keys")
	}
	return nil
}

// checkForwardHeadersGate returns a health-check error when any enforced
// binding depends on a header that is not always forwarded to plugins and
// the "Forward Grafana HTTP Headers" toggle is off. Returns nil when the
// configuration is consistent.
func checkForwardHeadersGate(s Settings) *backend.CheckHealthResult {
	if s.ForwardGrafanaHeaders {
		return nil
	}
	var offenders []string
	for _, cs := range s.CustomSettings {
		if !cs.Enforced {
			continue
		}
		var header string
		switch cs.Source {
		case customSettingSourceHeader:
			header = http.CanonicalHeaderKey(cs.HeaderName)
		case CustomSettingSourceJWT:
			name := cs.JWTHeaderName
			if name == "" {
				name = defaultJWTHeaderName
			}
			header = http.CanonicalHeaderKey(name)
		default:
			continue
		}
		if header == "" || headerIsAlwaysForwarded(header) {
			continue
		}
		offenders = append(offenders, fmt.Sprintf("%q (header %q)", cs.Setting, header))
	}
	if len(offenders) == 0 {
		return nil
	}
	return &backend.CheckHealthResult{
		Status: backend.HealthStatusError,
		Message: fmt.Sprintf(
			"Enforced setting(s) %s depend on a header that Grafana does not forward to plugins by default. "+
				"Enable \"Forward Grafana HTTP Headers\" on the datasource, or point the binding at one of the "+
				"always-forwarded headers (X-Grafana-Id, X-Dashboard-Uid, X-Panel-Id, X-Rule-Uid, X-Datasource-Uid).",
			strings.Join(offenders, ", "),
		),
	}
}
