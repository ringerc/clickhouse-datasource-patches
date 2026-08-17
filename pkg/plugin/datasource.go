package plugin

import (
	"context"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	schemas "github.com/grafana/schemads"
	"github.com/grafana/sqlds/v5"
)

// clickhouseInstance wraps the sqlds-managed instance so its Dispose also
// closes the SchemaProvider's shared *sql.DB. Embedding *sqlds.SQLDatasource
// promotes every handler method (QueryData, CheckHealth, CallResource, …) so
// type assertions on instancemgmt.Instance keep working unchanged.
type clickhouseInstance struct {
	*sqlds.SQLDatasource
	schema *SchemaProvider
}

func (i *clickhouseInstance) Dispose() {
	i.SQLDatasource.Dispose()
	if err := i.schema.Close(); err != nil {
		backend.Logger.Error("failed to close schema provider", "error", err)
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
		// DB will then leak on settings change but the plugin still works.
		return inst, nil
	}
	return &clickhouseInstance{SQLDatasource: sqlInst, schema: schemaProvider}, nil
}

// logEnforcedSettingsStartup emits a single Info line summarising the enforced-settings
// configuration at datasource instance creation time. Names only — no values.
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
}
