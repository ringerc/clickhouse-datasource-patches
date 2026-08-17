package plugin

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	schemas "github.com/grafana/schemads"
	"github.com/grafana/sqlds/v5"
)

// alwaysForwardedHeaders enumerates the HTTP headers Grafana forwards to
// backend plugins regardless of the datasource's "Forward Grafana HTTP
// Headers" toggle. These are set by Grafana core middleware that runs
// before ClearAuthHeadersMiddleware and independently of sqlds's
// grafana-http-headers pass-through path.
//
// Keep this list conservative: only add headers whose forwarding is
// unconditional in current Grafana OSS. Toggle-gated headers
// (Authorization, X-Id-Token, Cookie, X-Grafana-User, and any custom
// headers) MUST NOT be included — a header/JWT-source binding that
// depends on those must trigger a "toggle required" warning.
//
// Sources:
//   - X-Grafana-Id: ForwardIDMiddleware (feature-toggled but forwarded
//     unconditionally on the plugin path when the toggle is on; treated
//     as trusted for freshness because Grafana re-mints it per request).
//   - X-Dashboard-Uid, X-Panel-Id, X-Rule-Uid, X-Datasource-Uid: set by
//     the query-context middleware and reach the plugin regardless of
//     the datasource toggle.
var alwaysForwardedHeaders = map[string]struct{}{
	http.CanonicalHeaderKey("X-Grafana-Id"):     {},
	http.CanonicalHeaderKey("X-Dashboard-Uid"):  {},
	http.CanonicalHeaderKey("X-Panel-Id"):       {},
	http.CanonicalHeaderKey("X-Rule-Uid"):       {},
	http.CanonicalHeaderKey("X-Datasource-Uid"): {},
}

// headerIsAlwaysForwarded reports whether Grafana forwards headerName to
// backend plugins without the datasource "Forward Grafana HTTP Headers"
// toggle. headerName should already be http.CanonicalHeaderKey-normalised.
func headerIsAlwaysForwarded(headerName string) bool {
	_, ok := alwaysForwardedHeaders[headerName]
	return ok
}

// clickhouseInstance wraps the sqlds-managed instance so its Dispose also
// closes the SchemaProvider's shared *sql.DB and terminates the JWKS cache's
// background refresh goroutines. Embedding *sqlds.SQLDatasource promotes every
// handler method (QueryData, CheckHealth, CallResource, …) so type assertions
// on instancemgmt.Instance keep working unchanged.
type clickhouseInstance struct {
	*sqlds.SQLDatasource
	schema *SchemaProvider
	plugin *Clickhouse
}

func (i *clickhouseInstance) Dispose() {
	i.SQLDatasource.Dispose()
	if err := i.schema.Close(); err != nil {
		backend.Logger.Error("failed to close schema provider", "error", err)
	}
	if i.plugin != nil {
		i.plugin.jwksCache.close()
	}
}

// schemaResourceOptions maps the plugin's enableSchemaCache and
// schemaCacheTTLSeconds settings onto the schemads response-cache options so
// the schema resource endpoints honour the same knobs as the SchemaProvider's
// sub-fetch caches (see NewSchemaProvider in schema.go). Without this the
// schemads defaults apply and the settings silently stop working.
func schemaResourceOptions(settings Settings) schemas.Options {
	if !settings.EnableSchemaCache {
		return schemas.Options{DisableCache: true}
	}
	opts := schemas.DefaultOptions
	if ttl := time.Duration(settings.SchemaCacheTTLSeconds) * time.Second; ttl > 0 {
		// Only the endpoints this plugin serves are overridden; ColumnValues
		// stays at the library default of uncached because its responses are
		// time-range dependent.
		opts.TTL.FullSchema = ttl
		opts.TTL.Tables = ttl
		opts.TTL.Columns = ttl
	}
	return opts
}

func NewDatasource(ctx context.Context, settings backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
	clickhousePlugin := Clickhouse{}
	s, settingsErr := LoadSettings(ctx, settings)
	if settingsErr == nil {
		clickhousePlugin.enforceReadOnly = s.EnforceReadOnly
		clickhousePlugin.enforcedStatic = buildEnforcedStaticChSettings(s)

		// Create the per-instance JWKS cache so JWT-sourced bindings that use
		// verify=jwks can share a single keyfunc per URL across all bindings.
		cache := newJWKSCache(nil) // nil → http.DefaultClient
		clickhousePlugin.jwksCache = cache

		rt := EnforcedSourceRuntime{JWKSCache: cache}
		bindings, bErr := s.enforcedBindingsWithRuntime(rt)
		if bErr == nil {
			clickhousePlugin.enforcedBindings = bindings
			for _, b := range bindings {
				if b.Source.Kind() != customSettingSourceStatic {
					clickhousePlugin.hasDynamicBindings = true
					break
				}
			}
		} else {
			// Should be unreachable — LoadSettings already called enforcedBindings()
			// and would have returned an error. Guard defensively.
			backend.Logger.Error("clickhouse datasource: failed to build enforced bindings at startup", "error", bErr)
		}
		logEnforcedSettingsStartup(s)
	}
	ds := sqlds.NewDatasource(&clickhousePlugin)

	// Wire enforced-settings health probes to run after the basic connectivity check.
	if settingsErr == nil && s.shouldForceReadOnly() {
		ds.PostCheckHealth = makeEnforcedSettingsHealthCheck(s, settings)
	}

	// Replace sqlds's default sqlutil.Interpolate pipeline with the
	// macropro-backed interpolator; see interpolateMacros in driver.go.
	ds.Interpolator = interpolateMacros
	pluginSettings := clickhousePlugin.Settings(ctx, settings)
	if pluginSettings.ForwardHeaders {
		ds.EnableMultipleConnections = true
	}

	schemaProvider := NewSchemaProvider(ctx, &clickhousePlugin, settings)
	// Mirror NewSchemaProvider: if the settings cannot be parsed, degrade to
	// no response caching rather than caching with defaults the user cannot
	// influence.
	schemaOptions := schemas.Options{DisableCache: true}
	if parsed, err := LoadSettings(ctx, settings); err == nil {
		schemaOptions = schemaResourceOptions(parsed)
	}
	ds.ResourceMiddleware = func(next backend.CallResourceHandler) backend.CallResourceHandler {
		return schemas.NewSchemaDatasourceWithOptions(
			schemaProvider,
			schemaProvider,
			schemaProvider,
			nil, // no table parameter values handler
			schemaProvider,
			next,
			schemaOptions,
		)
	}

	inst, err := ds.NewDatasource(ctx, settings)
	if err != nil {
		return nil, err
	}
	sqlInst, ok := inst.(*sqlds.SQLDatasource)
	if !ok {
		// Defensive: if sqlds ever returns a different concrete type we can't
		// embed it cleanly. Fall back to the unwrapped instance — the schema
		// DB and JWKS cache will then leak on settings change but the plugin
		// still works. Surface this via an error log so operators notice the
		// degraded lifecycle instead of it happening silently.
		backend.Logger.Error(
			"clickhouse datasource: sqlds returned an unexpected concrete type; "+
				"schema DB and JWKS cache will not be cleaned up on Dispose",
			"type", fmt.Sprintf("%T", inst),
		)
		return inst, nil
	}
	return &clickhouseInstance{SQLDatasource: sqlInst, schema: schemaProvider, plugin: &clickhousePlugin}, nil
}

// logEnforcedSettingsStartup emits a summary of the enforced-settings configuration
// at datasource instance creation time. Names only — no values. Also emits Warn
// lines for trust-model risks that operators should notice at startup:
//   - dynamic-source bindings on headers that require the "Forward Grafana HTTP
//     Headers" toggle when the toggle is off;
//   - JWT-source bindings with verify=none on any header other than X-Grafana-Id
//     (Grafana does not re-verify forwarded IdP tokens; they may be stale).
func logEnforcedSettingsStartup(s Settings) {
	if !s.shouldForceReadOnly() {
		return
	}
	names := make([]string, 0)
	headerNames := make([]string, 0)
	jwtNames := make([]string, 0)
	for _, cs := range s.CustomSettings {
		if cs.Enforced {
			names = append(names, cs.Setting)
			switch cs.Source {
			case customSettingSourceHeader:
				headerNames = append(headerNames, cs.Setting)
			case CustomSettingSourceJWT:
				jwtNames = append(jwtNames, cs.Setting)
			}
		}
	}
	backend.Logger.Info("clickhouse datasource: enforced settings active",
		"enforced_setting_count", len(names),
		"enforced_setting_names", strings.Join(names, ","),
		"enforce_readonly", s.EnforceReadOnly,
		"header_sourced_setting_count", len(headerNames),
		"header_sourced_setting_names", strings.Join(headerNames, ","),
		"jwt_sourced_setting_count", len(jwtNames),
		"jwt_sourced_setting_names", strings.Join(jwtNames, ","),
	)

	// Warn about headers that will not reach the plugin without the toggle.
	if !s.ForwardGrafanaHeaders {
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
			backend.Logger.Warn(
				"clickhouse datasource: enforced-settings binding depends on a header that is not always "+
					"forwarded to plugins; enable \"Forward Grafana HTTP Headers\" on the datasource, "+
					"or the OnMissing policy will decide every request",
				"setting", cs.Setting,
				"source", cs.Source,
				"header", header,
			)
		}
	}

	// Warn about JWT bindings that skip signature verification on a header
	// other than X-Grafana-Id. Grafana validates upstream OAuth tokens only
	// at login; forwarded tokens are cached and may be stale by the time
	// they reach the plugin. See docs "Trust model".
	for _, cs := range s.CustomSettings {
		if !cs.Enforced || cs.Source != CustomSettingSourceJWT {
			continue
		}
		verify := cs.JWTVerify
		if verify == "" {
			verify = CustomSettingJWTVerifyNone
		}
		if verify != CustomSettingJWTVerifyNone {
			continue
		}
		name := cs.JWTHeaderName
		if name == "" {
			name = defaultJWTHeaderName
		}
		if http.CanonicalHeaderKey(name) == defaultJWTHeaderName {
			continue
		}
		backend.Logger.Warn(
			"clickhouse datasource: enforced-settings JWT binding uses jwtVerify=none on a header other "+
				"than X-Grafana-Id; the plugin will enforce exp but cannot verify signature. Prefer "+
				"jwtVerify=jwks for tokens issued by an upstream IdP",
			"setting", cs.Setting,
			"header", http.CanonicalHeaderKey(name),
		)
	}
}
