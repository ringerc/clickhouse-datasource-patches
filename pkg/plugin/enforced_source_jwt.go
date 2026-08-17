package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	keyfunc "github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"golang.org/x/time/rate"
)

// ---------------------------------------------------------------------------
// JWKS cache
// ---------------------------------------------------------------------------

const (
	jwksRefreshInterval = 15 * time.Minute
	jwksFetchTimeout    = 5 * time.Second
	jwksFailureCacheTTL = 30 * time.Second
)

// jwksCache is a per-datasource-instance cache of keyfunc.Keyfunc instances
// keyed by JWKS URL. Multiple JWT-sourced bindings that point at the same URL
// share a single Keyfunc (and its background refresh goroutine).
//
// Negative-failure entries are retained for jwksFailureCacheTTL to avoid
// hammering an unreachable JWKS endpoint; after the TTL the next call retries.
//
// The cache owns a cancellable context that is passed to
// keyfunc.NewDefaultOverrideCtx so background refresh goroutines terminate
// when close() is called (typically from clickhouseInstance.Dispose).
type jwksCache struct {
	mu        sync.Mutex
	entries   map[string]keyfunc.Keyfunc
	failures  map[string]time.Time
	client    *http.Client
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func newJWKSCache(client *http.Client) *jwksCache {
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &jwksCache{
		entries:  make(map[string]keyfunc.Keyfunc),
		failures: make(map[string]time.Time),
		client:   client,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// close cancels the background refresh context for every Keyfunc created via
// this cache, terminating their goroutines. Safe to call multiple times; nil-safe.
func (c *jwksCache) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
	})
}

// getOrCreate returns the cached keyfunc.Keyfunc for url, creating it on
// first use. On failure the error is cached for jwksFailureCacheTTL.
func (c *jwksCache) getOrCreate(url string) (keyfunc.Keyfunc, error) {
	c.mu.Lock()
	if kf, ok := c.entries[url]; ok {
		c.mu.Unlock()
		return kf, nil
	}
	if failTime, ok := c.failures[url]; ok {
		elapsed := time.Since(failTime)
		if elapsed < jwksFailureCacheTTL {
			c.mu.Unlock()
			return nil, fmt.Errorf("JWKS endpoint %q temporarily unavailable (last fetch failed %v ago; retry in %v)",
				url, elapsed.Round(time.Second), (jwksFailureCacheTTL - elapsed).Round(time.Second))
		}
		// TTL expired; clear the failure entry and retry.
		delete(c.failures, url)
	}
	c.mu.Unlock()

	// Build outside the lock; initial HTTP fetch may take up to jwksFetchTimeout.
	kf, err := buildJWKSKeyfunc(c.ctx, url, c.client)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if _, ok := c.entries[url]; !ok {
			c.failures[url] = time.Now()
		}
		return nil, fmt.Errorf("JWKS fetch failed for %q: %w", url, err)
	}
	// Double-check: another goroutine may have created it while we built.
	if existing, ok := c.entries[url]; ok {
		return existing, nil
	}
	c.entries[url] = kf
	return kf, nil
}

// buildJWKSKeyfunc creates a keyfunc.Keyfunc for url. The initial HTTP fetch
// is performed synchronously (up to jwksFetchTimeout); a background goroutine
// refreshes the key set every jwksRefreshInterval thereafter. The goroutine
// runs until ctx is cancelled — pass the jwksCache-owned ctx so Dispose can
// terminate it.
func buildJWKSKeyfunc(ctx context.Context, url string, client *http.Client) (keyfunc.Keyfunc, error) {
	noErrFirst := false // fail fast on initial fetch failure
	unk := rate.NewLimiter(rate.Every(5*time.Minute), 1)
	override := keyfunc.Override{
		Client:                    client,
		HTTPTimeout:               jwksFetchTimeout,
		RefreshInterval:           jwksRefreshInterval,
		RefreshUnknownKID:         unk,
		NoErrorReturnFirstHTTPReq: &noErrFirst,
	}
	return keyfunc.NewDefaultOverrideCtx(ctx, []string{url}, override)
}

// ---------------------------------------------------------------------------
// jwtValueSource
// ---------------------------------------------------------------------------

// jwtValueSource implements EnforcedValueSource for source="jwt". It reads a
// JWT from a named forwarded HTTP header, optionally verifies its signature
// using a JWKS endpoint, and extracts a named claim by dotted path.
type jwtValueSource struct {
	settingName string     // for error / log messages
	headerName  string     // canonicalised HTTP header containing the JWT
	claimPath   []string   // pre-split dotted claim path
	joinSep     string     // separator when the claim value is an array
	verify      string     // "none" | "jwks"
	jwksURL     string     // JWKS endpoint URL (only when verify == "jwks")
	issuer      string     // optional expected iss (only when verify == "jwks")
	audience    string     // optional expected aud (only when verify == "jwks")
	cache       *jwksCache // shared JWKS cache; nil when verify == "none" or no runtime cache
}

// Kind implements EnforcedValueSource.
func (j *jwtValueSource) Kind() string { return CustomSettingSourceJWT }

// HeaderName returns the canonical HTTP header name that carries the JWT token.
// Used by health-check messaging.
func (j *jwtValueSource) HeaderName() string { return j.headerName }

// ClaimPath returns the dotted-path claim name for use in logs and health messages.
func (j *jwtValueSource) ClaimPath() string { return strings.Join(j.claimPath, ".") }

// Verify returns the verification mode ("none" or "jwks").
func (j *jwtValueSource) Verify() string { return j.verify }

// JWKSURL returns the configured JWKS URL, or empty string when verify == "none".
func (j *jwtValueSource) JWKSURL() string { return j.jwksURL }

// Resolve implements EnforcedValueSource.
func (j *jwtValueSource) Resolve(ctx context.Context) (string, bool, error) {
	headers := forwardedHeadersFromContext(ctx)
	if headers == nil {
		return "", false, nil
	}
	raw, ok := headers[j.headerName]
	if !ok || raw == "" {
		return "", false, nil
	}

	// Strip optional "Bearer " prefix (case-insensitive).
	token := raw
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		token = strings.TrimSpace(raw[7:])
	}
	if token == "" {
		return "", false, nil
	}

	var claims jwt.MapClaims
	var parsedToken *jwt.Token

	switch j.verify {
	case CustomSettingJWTVerifyNone:
		parser := jwt.NewParser(jwt.WithoutClaimsValidation())
		var mc jwt.MapClaims
		var parseErr error
		parsedToken, _, parseErr = parser.ParseUnverified(token, &mc)
		if parseErr != nil {
			// Under verify=none the operator has explicitly opted OUT of strict
			// verification. Treat a malformed token as "value absent" so the
			// OnMissing policy (reject | empty) decides the outcome — otherwise
			// OnMissing=empty would be silently ignored on broken tokens.
			backend.Logger.Warn("jwt in header is malformed; treating as absent (verify=none)",
				"setting", j.settingName,
				"header", j.headerName,
				"error", parseErr,
			)
			return "", false, nil
		}
		// Enforce `exp` even under verify=none for any header other than
		// X-Grafana-Id. Grafana validates upstream OAuth tokens at login
		// only — it does NOT re-verify at forward time, and cached IdP
		// tokens routinely outlive their `exp` by hours. Binding a
		// server-side ClickHouse setting to a stale claim is worse than
		// having no claim: treat expiry as "absent" so the OnMissing
		// policy decides. X-Grafana-Id is exempt because Grafana re-mints
		// it per request and its lifetime is a Grafana concern.
		if !isTrustedGrafanaHeader(j.headerName) {
			if expired, err := jwtIsExpired(mc); err != nil {
				backend.Logger.Warn("jwt in header has malformed exp claim; treating as absent (verify=none)",
					"setting", j.settingName,
					"header", j.headerName,
					"error", err,
				)
				return "", false, nil
			} else if expired {
				backend.Logger.Warn("jwt in header is expired; treating as absent (verify=none, non-trusted header)",
					"setting", j.settingName,
					"header", j.headerName,
				)
				return "", false, nil
			}
		}
		claims = mc

	case CustomSettingJWTVerifyJWKS:
		if j.cache == nil {
			return "", false, fmt.Errorf("jwt JWKS unavailable for setting %q: datasource was not initialised with a JWKS cache",
				j.settingName)
		}
		kf, err := j.cache.getOrCreate(j.jwksURL)
		if err != nil {
			backend.Logger.Warn("jwt JWKS unavailable",
				"setting", j.settingName,
				"header", j.headerName,
				"error", err,
			)
			return "", false, backend.DownstreamError(fmt.Errorf(
				"jwt JWKS unavailable for header %q setting %q: %w",
				j.headerName, j.settingName, err))
		}

		opts := []jwt.ParserOption{jwt.WithIssuedAt()}
		if j.issuer != "" {
			opts = append(opts, jwt.WithIssuer(j.issuer))
		}
		if j.audience != "" {
			opts = append(opts, jwt.WithAudience(j.audience))
		}

		var mc jwt.MapClaims
		var parseErr error
		parsedToken, parseErr = jwt.NewParser(opts...).ParseWithClaims(token, &mc, kf.Keyfunc)
		if parseErr != nil {
			cause := categoriseJWTError(parseErr)
			backend.Logger.Warn("jwt verify failure",
				"setting", j.settingName,
				"header", j.headerName,
				"cause", cause,
			)
			return "", false, backend.DownstreamError(fmt.Errorf(
				"jwt verification failed for header %q setting %q: %s",
				j.headerName, j.settingName, cause))
		}
		claims = mc

		// Log sub/iss — these identify the user and are safe to emit.
		sub, _ := parsedToken.Claims.GetSubject()
		iss, _ := parsedToken.Claims.GetIssuer()
		if sub != "" || iss != "" {
			backend.Logger.Debug("jwt verified",
				"setting", j.settingName, "sub", sub, "iss", iss)
		}
	}

	// Walk the dotted claim path.
	val, found, err := extractJWTClaim(claims, j.claimPath, j.joinSep, j.settingName)
	if err != nil {
		return "", false, backend.DownstreamError(err)
	}
	if !found {
		return "", false, nil
	}

	// Log resolution metadata (no claim value).
	var sub, iss string
	if parsedToken != nil && parsedToken.Claims != nil {
		sub, _ = parsedToken.Claims.GetSubject()
		iss, _ = parsedToken.Claims.GetIssuer()
	}
	backend.Logger.Debug("resolved enforced setting from jwt",
		"setting", j.settingName,
		"header", j.headerName,
		"claim", j.ClaimPath(),
		"sub", sub,
		"iss", iss,
	)
	return val, true, nil
}

// isTrustedGrafanaHeader reports whether the (canonicalised) header name is
// one that Grafana mints itself per request and whose freshness is therefore
// Grafana's concern rather than the plugin's. Currently only X-Grafana-Id
// qualifies; all other headers may carry a token that was issued by an
// upstream IdP and cached by Grafana beyond its `exp`.
func isTrustedGrafanaHeader(headerName string) bool {
	return headerName == defaultJWTHeaderName
}

// jwtIsExpired returns (true, nil) when the token's `exp` claim is in the
// past (with a 60s leeway). It returns (false, nil) when the claim is absent
// (RFC 7519 makes `exp` optional). A malformed `exp` yields an error so the
// caller can log it — treating a broken exp as "not expired" would defeat
// the freshness check.
func jwtIsExpired(claims jwt.MapClaims) (bool, error) {
	raw, ok := claims["exp"]
	if !ok {
		return false, nil
	}
	var expUnix int64
	switch v := raw.(type) {
	case float64:
		expUnix = int64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return false, fmt.Errorf("exp is not an integer: %w", err)
		}
		expUnix = n
	default:
		return false, fmt.Errorf("exp has unsupported type %T", raw)
	}
	const leeway = 60 * time.Second
	return time.Now().Add(-leeway).After(time.Unix(expUnix, 0)), nil
}

// categoriseJWTError maps a jwt parse/verify error to a short descriptive reason.
func categoriseJWTError(err error) string {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return "token expired"
	case errors.Is(err, jwt.ErrTokenSignatureInvalid) || errors.Is(err, jwt.ErrTokenUnverifiable):
		return "signature invalid"
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return "iss mismatch"
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return "aud mismatch"
	case errors.Is(err, jwt.ErrTokenMalformed):
		return "token malformed"
	default:
		msg := err.Error()
		if strings.Contains(msg, "kid") {
			return "unknown kid"
		}
		if strings.Contains(msg, "keyfunc") || strings.Contains(msg, "JWKS") || strings.Contains(msg, "jwks") {
			return "jwks fetch failed"
		}
		return "verification failed"
	}
}

// ---------------------------------------------------------------------------
// Claim extraction
// ---------------------------------------------------------------------------

// extractJWTClaim walks claims by dotted path segments and returns the
// converted string value.
//   - Returns ("", false, nil)  when any segment along the path is absent or nil.
//   - Returns ("", false, err)  when the leaf or an intermediate is an unsupported type.
//   - Returns (val, true, nil)  on success.
func extractJWTClaim(claims jwt.MapClaims, path []string, joinSep, settingName string) (string, bool, error) {
	var cur interface{} = map[string]interface{}(claims)
	for i, seg := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return "", false, nil // intermediate was not a map
		}
		val, exists := m[seg]
		if !exists {
			return "", false, nil // segment absent
		}
		if i == len(path)-1 {
			return convertClaimValue(val, joinSep, settingName, strings.Join(path, "."))
		}
		cur = val
	}
	return "", false, nil
}

// convertClaimValue converts a JWT leaf claim value to its string representation.
func convertClaimValue(v interface{}, joinSep, settingName, claimDotted string) (string, bool, error) {
	switch t := v.(type) {
	case nil:
		return "", false, nil
	case string:
		return t, true, nil
	case float64:
		return fmt.Sprint(t), true, nil
	case bool:
		if t {
			return "true", true, nil
		}
		return "false", true, nil
	case json.Number:
		return t.String(), true, nil
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, elem := range t {
			switch ev := elem.(type) {
			case nil:
				// skip nil elements in arrays
			case string:
				parts = append(parts, ev)
			case float64:
				parts = append(parts, fmt.Sprint(ev))
			case bool:
				if ev {
					parts = append(parts, "true")
				} else {
					parts = append(parts, "false")
				}
			case map[string]interface{}, []interface{}:
				return "", false, fmt.Errorf(
					"jwt claim %q for setting %q contains nested objects in array; not supported",
					claimDotted, settingName)
			default:
				return "", false, fmt.Errorf(
					"jwt claim %q for setting %q has unsupported array element type %T",
					claimDotted, settingName, elem)
			}
		}
		return strings.Join(parts, joinSep), true, nil
	case map[string]interface{}:
		return "", false, fmt.Errorf(
			"jwt claim %q for setting %q is an object; not supported",
			claimDotted, settingName)
	default:
		return "", false, fmt.Errorf(
			"jwt claim %q for setting %q has unsupported type %T",
			claimDotted, settingName, v)
	}
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

// jwtValueSourceFactory constructs a jwtValueSource from cs.
// Belt-and-braces checks mirror the validation already done in LoadSettings.
func jwtValueSourceFactory(cs CustomSetting, rt EnforcedSourceRuntime) (EnforcedValueSource, error) {
	if !cs.Enforced {
		return nil, fmt.Errorf("source=jwt requires enforced=true")
	}
	if cs.JWTClaim == "" {
		return nil, fmt.Errorf("source=jwt requires jwtClaim")
	}
	if cs.Value != "" {
		return nil, fmt.Errorf("source=jwt must not set value")
	}
	if strings.EqualFold(cs.Setting, "readonly") {
		return nil, fmt.Errorf("source=jwt must not bind to reserved setting %q", cs.Setting)
	}

	headerName := cs.JWTHeaderName
	if headerName == "" {
		headerName = defaultJWTHeaderName
	}
	headerName = http.CanonicalHeaderKey(headerName)

	verify := cs.JWTVerify
	if verify == "" {
		verify = CustomSettingJWTVerifyNone
	}

	joinSep := cs.JWTClaimJoin
	if joinSep == "" {
		joinSep = defaultJWTClaimJoin
	}

	if verify == CustomSettingJWTVerifyJWKS && cs.JWTJWKSURL == "" {
		return nil, fmt.Errorf("source=jwt with verify=jwks requires jwtJwksUrl")
	}

	src := &jwtValueSource{
		settingName: cs.Setting,
		headerName:  headerName,
		claimPath:   strings.Split(cs.JWTClaim, "."),
		joinSep:     joinSep,
		verify:      verify,
		jwksURL:     cs.JWTJWKSURL,
		issuer:      cs.JWTIssuer,
		audience:    cs.JWTAudience,
		cache:       rt.JWKSCache, // nil on validation / health-check path
	}
	return src, nil
}

func init() {
	RegisterEnforcedValueSourceFactory(CustomSettingSourceJWT, jwtValueSourceFactory)
}
