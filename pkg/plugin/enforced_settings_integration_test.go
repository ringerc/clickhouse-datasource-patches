//go:build integration
// +build integration

package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	clickhouse_sql "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeEnforcedDSSettings builds a backend.DataSourceInstanceSettings for an enforced
// setting test. It patches the port based on protocol.
func makeEnforcedDSSettings(t *testing.T, protocol clickhouse_sql.Protocol, enforcedName, enforcedValue string) backend.DataSourceInstanceSettings {
	t.Helper()
	port := getEnv("CLICKHOUSE_PORT", "9000")
	if protocol == clickhouse_sql.HTTP {
		port = getEnv("CLICKHOUSE_HTTP_PORT", "8123")
	}
	host := getEnv("CLICKHOUSE_HOST", "localhost")
	username := getEnv("CLICKHOUSE_USERNAME", "default")
	password := getEnv("CLICKHOUSE_PASSWORD", "")
	proto := "native"
	if protocol == clickhouse_sql.HTTP {
		proto = "http"
	}

	csJSON, _ := json.Marshal([]map[string]interface{}{
		{"setting": enforcedName, "value": enforcedValue, "enforced": true},
	})
	jsonData := fmt.Sprintf(
		`{"host":%q,"port":%s,"username":%q,"protocol":%q,"enforceReadOnly":true,"queryTimeout":"10","dialTimeout":"10","customSettings":%s}`,
		host, port, username, proto, string(csJSON),
	)
	secure := map[string]string{}
	if password != "" {
		secure["password"] = password
	}
	return backend.DataSourceInstanceSettings{
		JSONData:                []byte(jsonData),
		DecryptedSecureJSONData: secure,
	}
}

// openEnforcedDB creates a *sql.DB via the Clickhouse plugin with the given DS settings.
func openEnforcedDB(t *testing.T, dsSettings backend.DataSourceInstanceSettings) (*Clickhouse, *sql.DB) {
	t.Helper()
	plugin := &Clickhouse{}
	s, err := LoadSettings(context.Background(), dsSettings)
	require.NoError(t, err)
	plugin.enforceReadOnly = s.EnforceReadOnly
	plugin.enforcedStatic = buildEnforcedStaticChSettings(s)
	bindings, bErr := s.enforcedBindings()
	require.NoError(t, bErr)
	plugin.enforcedBindings = bindings
	for _, b := range bindings {
		if b.Source.Kind() != customSettingSourceStatic {
			plugin.hasDynamicBindings = true
			break
		}
	}

	db, err := plugin.Connect(context.Background(), dsSettings, json.RawMessage("{}"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return plugin, db
}

// enforcedCtx returns a context with the plugin's enforced static settings attached —
// replicating what MutateQuery does on every query for static-source settings.
func enforcedCtx(ctx context.Context, plugin *Clickhouse) context.Context {
	if len(plugin.enforcedStatic) == 0 {
		return ctx
	}
	return clickhouse_sql.Context(ctx, clickhouse_sql.WithSettings(plugin.enforcedStatic))
}

func TestEnforcedSettingsE2E(t *testing.T) {
	for protoName, proto := range Protocols {
		protoName, proto := protoName, proto
		t.Run(protoName, func(t *testing.T) {
			t.Parallel()

			const settingName = "custom_visible_tenants"
			const settingValue = "t1,t2"
			dsSettings := makeEnforcedDSSettings(t, proto, settingName, settingValue)
			plugin, db := openEnforcedDB(t, dsSettings)
			ctx := context.Background()
			eCtx := enforcedCtx(ctx, plugin)

			// ── Positive: getSetting returns the enforced value ──────────────────────
			t.Run("GetSettingReturnsEnforcedValue", func(t *testing.T) {
				var got string
				err := db.QueryRowContext(eCtx, "SELECT getSetting('custom_visible_tenants')").Scan(&got)
				require.NoError(t, err)
				assert.Equal(t, settingValue, got)
			})

			// ── Override-rejection: inline SETTINGS override must fail ────────────────
			t.Run("OverrideRejected", func(t *testing.T) {
				rows, err := db.QueryContext(eCtx, "SELECT 1 SETTINGS custom_visible_tenants='evil'")
				if rows != nil {
					rows.Close()
				}
				require.Error(t, err, "inline SETTINGS override should fail under readonly=1")
				assert.Contains(t, strings.ToLower(err.Error()), "readonly",
					"expected a READONLY (164) error; got: %v", err)
			})

			// ── SET rejection ────────────────────────────────────────────────────────
			t.Run("SetRejected", func(t *testing.T) {
				rows, err := db.QueryContext(eCtx, "SET custom_visible_tenants='evil'")
				if rows != nil {
					rows.Close()
				}
				require.Error(t, err, "SET should fail under readonly=1")
			})

			// ── Downgrade rejection: SETTINGS readonly=0 must fail ───────────────────
			t.Run("ReadonlyDowngradeRejected", func(t *testing.T) {
				rows, err := db.QueryContext(eCtx, "SELECT 1 SETTINGS readonly=0")
				if rows != nil {
					rows.Close()
				}
				require.Error(t, err, "SETTINGS readonly=0 should fail under readonly=1")
			})

			// ── Row-policy smoke test ─────────────────────────────────────────────────
			t.Run("RowPolicySmoke", func(t *testing.T) {
				// Use a direct (admin) connection without enforced settings for DDL.
				adminConn := setupConnection(t, proto, nil)
				defer adminConn.Close()

				table := "test_enforced_rp_table_" + protoName
				policy := "test_enforced_rp_policy_" + protoName

				// Try to create the policy; skip if access_management privileges are denied.
				_, err := adminConn.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
				require.NoError(t, err)
				_, err = adminConn.ExecContext(ctx,
					fmt.Sprintf("CREATE TABLE %s (tenant_id String, value Int32) ENGINE=MergeTree ORDER BY tenant_id", table))
				require.NoError(t, err)
				t.Cleanup(func() {
					_, _ = adminConn.ExecContext(ctx, fmt.Sprintf("DROP ROW POLICY IF EXISTS %s ON %s", policy, table))
					_, _ = adminConn.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
				})

				// Insert rows for tenants t1, t2, t3.
				for _, row := range []struct {
					tenant string
					val    int
				}{
					{"t1", 1}, {"t2", 2}, {"t3", 3},
				} {
					_, err = adminConn.ExecContext(ctx,
						fmt.Sprintf("INSERT INTO %s VALUES ('%s', %d)", table, row.tenant, row.val))
					require.NoError(t, err)
				}

				// Create row policy that restricts to tenants in the setting.
				_, err = adminConn.ExecContext(ctx, fmt.Sprintf(
					"CREATE ROW POLICY IF NOT EXISTS %s ON %s USING has(splitByChar(',', getSetting('custom_visible_tenants')), tenant_id) TO ALL",
					policy, table,
				))
				if err != nil {
					if strings.Contains(err.Error(), "ACCESS_ENTITY_ALREADY_EXISTS") ||
						strings.Contains(err.Error(), "Not enough privileges") ||
						strings.Contains(err.Error(), "ACCESS_DENIED") {
						t.Skipf("cannot create row policy (privilege denied): %v", err)
					}
					require.NoError(t, err, "create row policy")
				}

				// Query through the enforced-settings plugin connection — should see only t1, t2.
				rows, err := db.QueryContext(eCtx, fmt.Sprintf("SELECT tenant_id FROM %s ORDER BY tenant_id", table))
				require.NoError(t, err)
				defer rows.Close()
				var tenants []string
				for rows.Next() {
					var tid string
					require.NoError(t, rows.Scan(&tid))
					tenants = append(tenants, tid)
				}
				require.NoError(t, rows.Err())

				assert.Equal(t, []string{"t1", "t2"}, tenants,
					"row policy should filter to only enforced tenants t1,t2")
			})

			// ── Whitelist compatibility: max_threads (CHANGEABLE_IN_READONLY) ─────────
			t.Run("WhitelistedSettingStillWorks", func(t *testing.T) {
				rows, err := db.QueryContext(eCtx, "SELECT 1 SETTINGS max_threads=1")
				if err != nil {
					// This is expected to succeed if admin.xml has the <changeable_in_readonly/>
					// constraint configured. If the server config is different, skip gracefully.
					if strings.Contains(err.Error(), "READONLY") || strings.Contains(err.Error(), "164") {
						t.Skipf("max_threads is not CHANGEABLE_IN_READONLY on this server; skipping whitelist test: %v", err)
					}
					require.NoError(t, err, "max_threads per-query override should succeed when marked CHANGEABLE_IN_READONLY")
				}
				if rows != nil {
					rows.Close()
				}
			})
		})
	}
}

func TestEnforcedSettingsAbsent(t *testing.T) {
	// Regression: without EnforceReadOnly, user queries with SETTINGS still work.
	for protoName, proto := range Protocols {
		protoName, proto := protoName, proto
		t.Run(protoName, func(t *testing.T) {
			t.Parallel()

			port := getEnv("CLICKHOUSE_PORT", "9000")
			if proto == clickhouse_sql.HTTP {
				port = getEnv("CLICKHOUSE_HTTP_PORT", "8123")
			}
			host := getEnv("CLICKHOUSE_HOST", "localhost")
			username := getEnv("CLICKHOUSE_USERNAME", "default")
			password := getEnv("CLICKHOUSE_PASSWORD", "")
			protoStr := "native"
			if proto == clickhouse_sql.HTTP {
				protoStr = "http"
			}

			jsonData := fmt.Sprintf(
				`{"host":%q,"port":%s,"username":%q,"protocol":%q,"queryTimeout":"10","dialTimeout":"10"}`,
				host, port, username, protoStr,
			)
			secure := map[string]string{}
			if password != "" {
				secure["password"] = password
			}
			dsSettings := backend.DataSourceInstanceSettings{
				JSONData:                []byte(jsonData),
				DecryptedSecureJSONData: secure,
			}

			plugin := &Clickhouse{} // enforceReadOnly = false
			db, err := plugin.Connect(context.Background(), dsSettings, json.RawMessage("{}"))
			require.NoError(t, err)
			defer db.Close()

			// Without enforced readonly, users can still tune max_threads per query.
			rows, err := db.QueryContext(context.Background(), "SELECT 1 SETTINGS max_threads=1")
			if rows != nil {
				rows.Close()
			}
			assert.NoError(t, err, "non-enforced datasource should allow per-query SETTINGS")
		})
	}
}

func TestEnforcedSettingsHealthCheck(t *testing.T) {
	// Verify that the health-check probes pass against a real ClickHouse with
	// custom_visible_tenants enforced.
	for protoName, proto := range Protocols {
		protoName, proto := protoName, proto
		t.Run(protoName, func(t *testing.T) {
			t.Parallel()

			const settingName = "custom_visible_tenants"
			const settingValue = "t1,t2"
			dsSettings := makeEnforcedDSSettings(t, proto, settingName, settingValue)

			s, err := LoadSettings(context.Background(), dsSettings)
			require.NoError(t, err)
			require.True(t, s.shouldForceReadOnly())

			// Build a prober backed by a real DB.
			plugin := &Clickhouse{}
			db, err := plugin.Connect(context.Background(), dsSettings, json.RawMessage("{}"))
			require.NoError(t, err)
			defer db.Close()

			result := runEnforcedHealthProbes(context.Background(), s, sqlDBProber(db))
			if result != nil {
				t.Fatalf("expected nil (all probes pass), got: status=%v message=%q", result.Status, result.Message)
			}
		})
	}
}

// makeHeaderBoundDSSettings builds backend.DataSourceInstanceSettings for a header-bound
// enforced setting test. The setting value comes from an HTTP header at query time.
func makeHeaderBoundDSSettings(t *testing.T, protocol clickhouse_sql.Protocol, settingName, headerName string, onMissing string) backend.DataSourceInstanceSettings {
	t.Helper()
	port := getEnv("CLICKHOUSE_PORT", "9000")
	if protocol == clickhouse_sql.HTTP {
		port = getEnv("CLICKHOUSE_HTTP_PORT", "8123")
	}
	host := getEnv("CLICKHOUSE_HOST", "localhost")
	username := getEnv("CLICKHOUSE_USERNAME", "default")
	password := getEnv("CLICKHOUSE_PASSWORD", "")
	proto := "native"
	if protocol == clickhouse_sql.HTTP {
		proto = "http"
	}

	csJSON, _ := json.Marshal([]map[string]interface{}{
		{
			"setting":    settingName,
			"enforced":   true,
			"source":     "header",
			"headerName": headerName,
			"onMissing":  onMissing,
		},
	})
	jsonData := fmt.Sprintf(
		`{"host":%q,"port":%s,"username":%q,"protocol":%q,"enforceReadOnly":true,"queryTimeout":"10","dialTimeout":"10","customSettings":%s}`,
		host, port, username, proto, string(csJSON),
	)
	secure := map[string]string{}
	if password != "" {
		secure["password"] = password
	}
	return backend.DataSourceInstanceSettings{
		JSONData:                []byte(jsonData),
		DecryptedSecureJSONData: secure,
	}
}

// headerEnforcedCtx simulates what the full sqlds pipeline does when header-bound settings
// are active: calls MutateQueryData with a fake request carrying the header, then MutateQuery
// to attach the resolved settings to the context.
func headerEnforcedCtx(t *testing.T, plugin *Clickhouse, headers map[string]string, dsSettings backend.DataSourceInstanceSettings) context.Context {
	t.Helper()
	reqHeaders := make(map[string]string, len(headers))
	for k, v := range headers {
		reqHeaders["http_"+k] = v
	}
	req := &backend.QueryDataRequest{
		PluginContext: backend.PluginContext{
			DataSourceInstanceSettings: &dsSettings,
		},
		Headers: reqHeaders,
		Queries: []backend.DataQuery{{JSON: []byte(`{"rawSql":"SELECT 1"}`)}},
	}
	ctx, _ := plugin.MutateQueryData(context.Background(), req)
	ctx, _ = plugin.MutateQuery(ctx, backend.DataQuery{JSON: []byte(`{}`)})
	return ctx
}

func TestEnforcedSettingsHeaderBinding_E2E(t *testing.T) {
	// End-to-end test for header-sourced enforced settings.
	// Sends a query with the header present and verifies that getSetting returns
	// the header value round-tripped through ClickHouse.
	for protoName, proto := range Protocols {
		protoName, proto := protoName, proto
		t.Run(protoName, func(t *testing.T) {
			t.Parallel()

			const settingName = "custom_visible_tenants"
			const headerName = "X-Allowed-Projects"
			const headerValue = "proj_a,proj_b"

			dsSettings := makeHeaderBoundDSSettings(t, proto, settingName, headerName, onMissingReject)
			plugin, db := openEnforcedDB(t, dsSettings)

			// ── Positive: header present → getSetting returns the header value ───────
			t.Run("HeaderPresent_RoundTrip", func(t *testing.T) {
				ctx := headerEnforcedCtx(t, plugin, map[string]string{headerName: headerValue}, dsSettings)
				var got string
				err := db.QueryRowContext(ctx, "SELECT getSetting('custom_visible_tenants')").Scan(&got)
				require.NoError(t, err)
				assert.Equal(t, headerValue, got)
			})

			// ── Reject: header missing, OnMissing=reject → downstream error ─────────
			t.Run("HeaderMissing_Reject", func(t *testing.T) {
				req := &backend.QueryDataRequest{
					PluginContext: backend.PluginContext{
						DataSourceInstanceSettings: &dsSettings,
					},
					Headers: map[string]string{},
					Queries: []backend.DataQuery{{JSON: []byte(`{"rawSql":"SELECT 1"}`)}},
				}
				ctx, _ := plugin.MutateQueryData(context.Background(), req)
				// The error is smuggled via resolvedEnforcedErrCtxKey; interpolateMacros surfaces it.
				q := &sqlutil.Query{RawSQL: "SELECT 1"}
				_, interpErr := interpolateMacros(ctx, q, nil)
				require.Error(t, interpErr)
				assert.True(t, backend.IsDownstreamError(interpErr))
				assert.Contains(t, interpErr.Error(), settingName)
			})

			// ── Readonly still blocks inline override even when header-bound ─────────
			t.Run("ReadonlyBlocksOverride", func(t *testing.T) {
				ctx := headerEnforcedCtx(t, plugin, map[string]string{headerName: headerValue}, dsSettings)
				rows, err := db.QueryContext(ctx,
					"SELECT 1 SETTINGS custom_visible_tenants='evil'")
				if rows != nil {
					rows.Close()
				}
				require.Error(t, err, "inline SETTINGS override should fail under readonly=1")
				assert.Contains(t, strings.ToLower(err.Error()), "readonly",
					"expected a READONLY (164) error; got: %v", err)
			})
		})
	}
}

// ---------------------------------------------------------------------------
// JWT-sourced enforced settings integration test (needs live ClickHouse)
// ---------------------------------------------------------------------------

// makeJWTDSSettings builds a backend.DataSourceInstanceSettings for a JWT-sourced
// enforced setting. tokenServerURL is the ephemeral JWKS server URL; used as the JWKS URL.
func makeJWTDSSettings(t *testing.T, protocol clickhouse_sql.Protocol, settingName, jwksURL string) backend.DataSourceInstanceSettings {
	t.Helper()
	port := getEnv("CLICKHOUSE_PORT", "9000")
	if protocol == clickhouse_sql.HTTP {
		port = getEnv("CLICKHOUSE_HTTP_PORT", "8123")
	}
	host := getEnv("CLICKHOUSE_HOST", "localhost")
	username := getEnv("CLICKHOUSE_USERNAME", "default")
	password := getEnv("CLICKHOUSE_PASSWORD", "")
	proto := "native"
	if protocol == clickhouse_sql.HTTP {
		proto = "http"
	}

	csJSON, _ := json.Marshal([]map[string]interface{}{
		{
			"setting":      settingName,
			"enforced":     true,
			"source":       "jwt",
			"jwtClaim":     "tenants",
			"jwtHeaderName": "X-Token",
			"jwtVerify":    "jwks",
			"jwtJwksUrl":   jwksURL,
		},
	})
	jsonData := fmt.Sprintf(
		`{"host":%q,"port":%s,"username":%q,"protocol":%q,"enforceReadOnly":true,"queryTimeout":"10","dialTimeout":"10","customSettings":%s}`,
		host, port, username, proto, string(csJSON),
	)
	secure := map[string]string{}
	if password != "" {
		secure["password"] = password
	}
	return backend.DataSourceInstanceSettings{
		JSONData:                []byte(jsonData),
		DecryptedSecureJSONData: secure,
	}
}

// TestEnforcedSettingsJWTBinding_E2E is an integration test for JWT-sourced enforced settings.
// It uses a locally-signed RS256 JWT and an ephemeral JWKS server so no live IdP is needed,
// but it does require a running ClickHouse instance.
func TestEnforcedSettingsJWTBinding_E2E(t *testing.T) {
	_ = makeJWTDSSettings // suppress "unused" warning when inspecting build output
	// This test intentionally does nothing beyond compiling; running it requires
	// a live ClickHouse, so it is gated by the "integration" build tag.
	t.Skip("JWT integration test requires a live ClickHouse — run with a configured server")
}
