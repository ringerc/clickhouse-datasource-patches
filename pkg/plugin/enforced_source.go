package plugin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// EnforcedValueSource resolves the effective value for one enforced setting
// on a per-request basis. Implementations must be safe for concurrent use.
//
// Registered instances are constructed once at datasource-instance creation
// time from a CustomSetting and then invoked for every query. The `static`
// and `header` sources are implemented in this package; future sources
// (e.g. "jwt") plug in via RegisterEnforcedValueSourceFactory.
type EnforcedValueSource interface {
	// Kind returns the source name (e.g. "static", "header"). Used for logs,
	// span attributes and query-comment tagging; never expose values.
	Kind() string
	// Resolve returns the effective value for this setting.
	//   ok=false + err==nil  → value absent; caller applies OnMissing.
	//   ok=true              → value is present (may be empty string).
	//   err!=nil             → hard failure; caller must fail the query.
	Resolve(ctx context.Context) (value string, ok bool, err error)
}

// EnforcedBinding pairs a resolved-per-query source with the setting it fills
// and the on-missing policy. Built once from a CustomSetting by BuildEnforcedBinding.
type EnforcedBinding struct {
	Setting   string
	OnMissing string // onMissingReject | onMissingEmpty
	Source    EnforcedValueSource
}

// EnforcedSourceRuntime carries plugin-level dependencies that some
// EnforcedValueSource implementations need at construction time. Static and
// header sources ignore it; the JWT source uses JWKSCache when Verify == "jwks".
// Pass EnforcedSourceRuntime{} (zero value) in contexts where the runtime
// dependencies are not available (validation, health checks) — those paths
// never call Resolve().
type EnforcedSourceRuntime struct {
	// JWKSCache is the per-datasource-instance JWKS key cache. Nil means JWT
	// sources with verify=jwks were built for inspection only and will return
	// an error if Resolve() is ever called.
	JWKSCache *jwksCache
}

// EnforcedValueSourceFactory validates a CustomSetting at load time and
// returns a ready-to-use EnforcedValueSource. Factories receive an
// EnforcedSourceRuntime that may be zero-valued on the validation / health-check
// path; implementations must tolerate a nil JWKSCache.
type EnforcedValueSourceFactory func(cs CustomSetting, rt EnforcedSourceRuntime) (EnforcedValueSource, error)

// enforcedValueSourceRegistry maps lowercase source names to their factories.
var enforcedValueSourceRegistry = map[string]EnforcedValueSourceFactory{}

// RegisterEnforcedValueSourceFactory registers a factory for the given source
// kind. Panics if kind is empty. Overwrites any existing registration (last
// write wins), which is intentional to allow test overrides.
func RegisterEnforcedValueSourceFactory(kind string, f EnforcedValueSourceFactory) {
	if kind == "" {
		panic("enforced value source kind must not be empty")
	}
	enforcedValueSourceRegistry[kind] = f
}

func init() {
	RegisterEnforcedValueSourceFactory(customSettingSourceStatic, staticSourceFactory)
	RegisterEnforcedValueSourceFactory(customSettingSourceHeader, headerSourceFactory)
}

// BuildEnforcedBinding constructs an EnforcedBinding from cs using rt for
// runtime dependencies. It normalises Source (empty → "static") and OnMissing
// (empty → "reject" for dynamic sources), then delegates to the registered
// factory. Pass EnforcedSourceRuntime{} when runtime dependencies are not
// needed (static/header sources) or not yet available (validation path).
func BuildEnforcedBinding(cs CustomSetting, rt EnforcedSourceRuntime) (EnforcedBinding, error) {
	src := cs.Source
	if src == "" {
		src = customSettingSourceStatic
	}

	factory, ok := enforcedValueSourceRegistry[src]
	if !ok {
		return EnforcedBinding{}, fmt.Errorf("unknown source %q; registered sources: %s",
			src, knownSources())
	}

	source, err := factory(cs, rt)
	if err != nil {
		return EnforcedBinding{}, err
	}

	onMissing := cs.OnMissing
	if onMissing == "" {
		if src == customSettingSourceStatic {
			// static never consults OnMissing; use reject as a safe default.
			onMissing = onMissingReject
		} else {
			onMissing = onMissingReject
		}
	}
	if onMissing != onMissingReject && onMissing != onMissingEmpty {
		return EnforcedBinding{}, fmt.Errorf("unknown onMissing value %q; accepted values: %q, %q",
			onMissing, onMissingReject, onMissingEmpty)
	}

	return EnforcedBinding{
		Setting:   cs.Setting,
		OnMissing: onMissing,
		Source:    source,
	}, nil
}

func knownSources() string {
	names := make([]string, 0, len(enforcedValueSourceRegistry))
	for k := range enforcedValueSourceRegistry {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}

// ---------------------------------------------------------------------------
// static source
// ---------------------------------------------------------------------------

type staticValueSource struct {
	value string
}

func (s staticValueSource) Kind() string { return customSettingSourceStatic }

func (s staticValueSource) Resolve(_ context.Context) (string, bool, error) {
	return s.value, true, nil
}

func staticSourceFactory(cs CustomSetting, _ EnforcedSourceRuntime) (EnforcedValueSource, error) {
	if cs.HeaderName != "" {
		return nil, fmt.Errorf("source=static must not set headerName")
	}
	if cs.OnMissing != "" {
		return nil, fmt.Errorf("source=static must not set onMissing")
	}
	return staticValueSource{value: cs.Value}, nil
}

// ---------------------------------------------------------------------------
// header source
// ---------------------------------------------------------------------------

type headerValueSource struct {
	headerName string // already canonicalised via http.CanonicalHeaderKey
}

func (h headerValueSource) Kind() string { return customSettingSourceHeader }

// HeaderName returns the canonical HTTP header name this source reads from the
// request context. Used by health-check messaging to name the header without
// exposing values.
func (h headerValueSource) HeaderName() string { return h.headerName }

// Resolve reads the forwarded-headers map from ctx. If the header is present
// (even with an empty string value) it is returned as ok=true. A present-but-empty
// header is treated as a real value; callers apply OnMissing only when the
// header key is absent from the map entirely.
func (h headerValueSource) Resolve(ctx context.Context) (string, bool, error) {
	headers := forwardedHeadersFromContext(ctx)
	if headers == nil {
		return "", false, nil
	}
	v, ok := headers[h.headerName]
	return v, ok, nil
}

func headerSourceFactory(cs CustomSetting, _ EnforcedSourceRuntime) (EnforcedValueSource, error) {
	if !cs.Enforced {
		return nil, fmt.Errorf("source=header requires enforced=true")
	}
	if cs.HeaderName == "" {
		return nil, fmt.Errorf("source=header requires headerName")
	}
	if cs.Value != "" {
		return nil, fmt.Errorf("source=header must not set value; value comes from the request header")
	}
	if strings.EqualFold(cs.Setting, "readonly") {
		return nil, fmt.Errorf("source=header must not bind to reserved setting %q", cs.Setting)
	}
	if cs.OnMissing != "" && cs.OnMissing != onMissingReject && cs.OnMissing != onMissingEmpty {
		return nil, fmt.Errorf("unknown onMissing value %q; accepted values: %q, %q",
			cs.OnMissing, onMissingReject, onMissingEmpty)
	}
	return headerValueSource{headerName: http.CanonicalHeaderKey(cs.HeaderName)}, nil
}

// ---------------------------------------------------------------------------
// Forwarded-headers context helpers
// ---------------------------------------------------------------------------

type forwardedHeadersCtxKeyType struct{}

var forwardedHeadersCtxKey = forwardedHeadersCtxKeyType{}

// WithForwardedHeaders returns ctx carrying the canonicalised forwarded
// headers map. Keys must already be http.CanonicalHeaderKey-normalised.
func WithForwardedHeaders(ctx context.Context, h map[string]string) context.Context {
	return context.WithValue(ctx, forwardedHeadersCtxKey, h)
}

// forwardedHeadersFromContext returns the headers map or nil.
func forwardedHeadersFromContext(ctx context.Context) map[string]string {
	m, _ := ctx.Value(forwardedHeadersCtxKey).(map[string]string)
	return m
}
