package plugin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/proxy"
	sdkconfig "github.com/grafana/grafana-plugin-sdk-go/config"
	"github.com/stretchr/testify/assert"
)

func TestLoadSettings(t *testing.T) {
	t.Run("should parse settings correctly", func(t *testing.T) {

		ctx := context.Background()
		ctx = sdkconfig.WithGrafanaConfig(ctx, sdkconfig.NewGrafanaCfg(map[string]string{
			"GF_SQL_ROW_LIMIT":                         "1000000",
			"GF_SQL_MAX_OPEN_CONNS_DEFAULT":            "10",
			"GF_SQL_MAX_IDLE_CONNS_DEFAULT":            "10",
			"GF_SQL_MAX_CONN_LIFETIME_SECONDS_DEFAULT": "60",
		}))

		type args struct {
			config backend.DataSourceInstanceSettings
		}
		tests := []struct {
			name         string
			args         args
			wantSettings Settings
			wantErr      error
			testCtx      context.Context
		}{
			{
				name: "should parse and set all json fields correctly",
				args: args{
					config: backend.DataSourceInstanceSettings{
						UID: "ds-uid",
						JSONData: []byte(`{
							"host": "foo", "port": 443,
							"path": "custom-path", "protocol": "http",
							"username": "baz",
							"defaultDatabase":"example", "tlsSkipVerify": true, "tlsAuth" : true,
							"tlsAuthWithCACert": true, "dialTimeout": "10", "enableSecureSocksProxy": true,
							"httpHeaders": [{ "name": " test-plain-1 ", "value": "value-1", "secure": false }],
							"forwardGrafanaHeaders": true,
							"enableRowLimit": true
						}`),
						DecryptedSecureJSONData: map[string]string{
							"password":  "bar",
							"tlsCACert": "caCert", "tlsClientCert": "clientCert", "tlsClientKey": "clientKey",
							"secureSocksProxyPassword":          "test",
							"secureHttpHeaders. test-secure-2 ": "value-2",
							"secureHttpHeaders.test-secure-3":   "value-3",
						},
					},
				},
				wantSettings: Settings{
					Host:               "foo",
					Port:               443,
					Path:               "custom-path",
					Protocol:           clickhouse.HTTP.String(),
					Username:           "baz",
					DefaultDatabase:    "example",
					InsecureSkipVerify: true,
					TlsClientAuth:      true,
					TlsAuthWithCACert:  true,
					Password:           "bar",
					TlsCACert:          "caCert",
					TlsClientCert:      "clientCert",
					TlsClientKey:       "clientKey",
					ConnMaxLifetime:    "5",
					DialTimeout:        "10",
					MaxIdleConns:       "25",
					MaxOpenConns:       "50",
					QueryTimeout:       "60",
					HttpHeaders: map[string]string{
						"test-plain-1":  "value-1",
						"test-secure-2": "value-2",
						"test-secure-3": "value-3",
					},
					ForwardGrafanaHeaders: true,
					ProxyOptions: &proxy.Options{
						Enabled: true,
						Auth: &proxy.AuthOptions{
							Username: "ds-uid",
							Password: "test",
						},
						Timeouts: &proxy.TimeoutOptions{
							Timeout:   10 * time.Second,
							KeepAlive: proxy.DefaultTimeoutOptions.KeepAlive,
						},
					},
					EnableRowLimit:        true,
					RowLimit:              1000000,
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should convert string values to the correct type",
				args: args{
					config: backend.DataSourceInstanceSettings{
						JSONData:                []byte(`{"host": "test", "port": "443", "path": "custom-path", "tlsSkipVerify": "true", "tlsAuth" : "true", "tlsAuthWithCACert": "true", "enableRowLimit": "true"}`),
						DecryptedSecureJSONData: map[string]string{},
					},
				},
				wantSettings: Settings{
					Host:               "test",
					Port:               443,
					Path:               "custom-path",
					InsecureSkipVerify: true,
					TlsClientAuth:      true,
					TlsAuthWithCACert:  true,
					ConnMaxLifetime:    "5",
					DialTimeout:        "10",
					MaxIdleConns:       "25",
					MaxOpenConns:       "50",
					QueryTimeout:       "60",
					ProxyOptions:          nil,
					EnableRowLimit:        true,
					RowLimit:              1000000,
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should parse v3 config fields into new fields",
				args: args{
					config: backend.DataSourceInstanceSettings{
						JSONData:                []byte(`{"server": "test", "port": 443, "timeout": "10", "enableRowLimit": true}`),
						DecryptedSecureJSONData: map[string]string{},
					},
				},
				wantSettings: Settings{
					Host:            "test",
					Port:            443,
					ConnMaxLifetime: "5",
					DialTimeout:     "10",
					MaxIdleConns:    "25",
					MaxOpenConns:    "50",
					QueryTimeout:          "60",
					RowLimit:              1000000,
					EnableRowLimit:        true,
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should disable row limit",
				args: args{
					config: backend.DataSourceInstanceSettings{
						UID: "ds-uid",
						JSONData: []byte(`{
							"host": "foo", "port": 443,
							"path": "custom-path", "protocol": "http",
							"username": "baz",
							"defaultDatabase":"example", "tlsSkipVerify": true, "tlsAuth" : true,
							"tlsAuthWithCACert": true, "dialTimeout": "10", "enableSecureSocksProxy": true,
							"httpHeaders": [{ "name": " test-plain-1 ", "value": "value-1", "secure": false }],
							"forwardGrafanaHeaders": true,
							"enableRowLimit": false
						}`),
						DecryptedSecureJSONData: map[string]string{
							"password":  "bar",
							"tlsCACert": "caCert", "tlsClientCert": "clientCert", "tlsClientKey": "clientKey",
							"secureSocksProxyPassword":          "test",
							"secureHttpHeaders. test-secure-2 ": "value-2",
							"secureHttpHeaders.test-secure-3":   "value-3",
						},
					},
				},
				wantSettings: Settings{
					Host:               "foo",
					Port:               443,
					Path:               "custom-path",
					Protocol:           clickhouse.HTTP.String(),
					Username:           "baz",
					DefaultDatabase:    "example",
					InsecureSkipVerify: true,
					TlsClientAuth:      true,
					TlsAuthWithCACert:  true,
					Password:           "bar",
					TlsCACert:          "caCert",
					TlsClientCert:      "clientCert",
					TlsClientKey:       "clientKey",
					ConnMaxLifetime:    "5",
					DialTimeout:        "10",
					MaxIdleConns:       "25",
					MaxOpenConns:       "50",
					QueryTimeout:       "60",
					HttpHeaders: map[string]string{
						"test-plain-1":  "value-1",
						"test-secure-2": "value-2",
						"test-secure-3": "value-3",
					},
					ForwardGrafanaHeaders: true,
					ProxyOptions: &proxy.Options{
						Enabled: true,
						Auth: &proxy.AuthOptions{
							Username: "ds-uid",
							Password: "test",
						},
						Timeouts: &proxy.TimeoutOptions{
							Timeout:   10 * time.Second,
							KeepAlive: proxy.DefaultTimeoutOptions.KeepAlive,
						},
					},
					EnableRowLimit:        false,
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should accept numeric dialTimeout and queryTimeout values",
				args: args{
					config: backend.DataSourceInstanceSettings{
						JSONData:                []byte(`{"host": "test", "port": 443, "dialTimeout": 15, "queryTimeout": 120}`),
						DecryptedSecureJSONData: map[string]string{},
					},
				},
				wantSettings: Settings{
					Host:            "test",
					Port:            443,
					ConnMaxLifetime: "5",
					DialTimeout:     "15",
					MaxIdleConns:    "25",
					MaxOpenConns:    "50",
					QueryTimeout:          "120",
					EnableRowLimit:        false,
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should accept numeric timeout value (v3 deprecated field)",
				args: args{
					config: backend.DataSourceInstanceSettings{
						JSONData:                []byte(`{"server": "test", "port": 443, "timeout": 25}`),
						DecryptedSecureJSONData: map[string]string{},
					},
				},
				wantSettings: Settings{
					Host:            "test",
					Port:            443,
					ConnMaxLifetime: "5",
					DialTimeout:     "25",
					MaxIdleConns:    "25",
					MaxOpenConns:    "50",
					QueryTimeout:          "60",
					EnableRowLimit:        false,
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should accept numeric timeout values with floating point precision",
				args: args{
					config: backend.DataSourceInstanceSettings{
						JSONData:                []byte(`{"host": "test", "port": 443, "dialTimeout": 10.5, "queryTimeout": 60.7}`),
						DecryptedSecureJSONData: map[string]string{},
					},
				},
				wantSettings: Settings{
					Host:            "test",
					Port:            443,
					ConnMaxLifetime: "5",
					DialTimeout:     "10",
					MaxIdleConns:    "25",
					MaxOpenConns:    "50",
					QueryTimeout:          "60",
					EnableRowLimit:        false,
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should trim whitespace from host",
				args: args{
					config: backend.DataSourceInstanceSettings{
						JSONData:                []byte(`{"host": "  ch.example.com  ", "port": 443}`),
						DecryptedSecureJSONData: map[string]string{},
					},
				},
				wantSettings: Settings{
					Host:                  "ch.example.com",
					Port:                  443,
					ConnMaxLifetime:       "5",
					DialTimeout:           "10",
					MaxIdleConns:          "25",
					MaxOpenConns:          "50",
					QueryTimeout:          "60",
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
			{
				name: "should trim whitespace from v3 server field",
				args: args{
					config: backend.DataSourceInstanceSettings{
						JSONData:                []byte(`{"server": "  ch.example.com  ", "port": 443}`),
						DecryptedSecureJSONData: map[string]string{},
					},
				},
				wantSettings: Settings{
					Host:                  "ch.example.com",
					Port:                  443,
					ConnMaxLifetime:       "5",
					DialTimeout:           "10",
					MaxIdleConns:          "25",
					MaxOpenConns:          "50",
					QueryTimeout:          "60",
					EnableSchemaCache:     true,
					SchemaCacheTTLSeconds: 60,
				},
				wantErr: nil,
				testCtx: ctx,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				gotSettings, err := LoadSettings(tt.testCtx, tt.args.config)
				assert.Equal(t, tt.wantErr, err)
				if !reflect.DeepEqual(gotSettings, tt.wantSettings) {
					t.Errorf("LoadSettings() = %v, want %v", gotSettings, tt.wantSettings)
				}
			})
		}
	})
	t.Run("should capture invalid settings", func(t *testing.T) {
		ctx := context.Background()
		ctx = sdkconfig.WithGrafanaConfig(ctx, sdkconfig.NewGrafanaCfg(map[string]string{
			"GF_SQL_ROW_LIMIT":                         "1000000",
			"GF_SQL_MAX_OPEN_CONNS_DEFAULT":            "10",
			"GF_SQL_MAX_IDLE_CONNS_DEFAULT":            "10",
			"GF_SQL_MAX_CONN_LIFETIME_SECONDS_DEFAULT": "60",
		}))

		tests := []struct {
			jsonData    string
			password    string
			wantErr     error
			description string
		}{
			{jsonData: `{ "host": "", "port": 443 }`, password: "", wantErr: ErrorMessageInvalidHost, description: "should capture empty server name"},
			{jsonData: `{ "host": "   ", "port": 443 }`, password: "", wantErr: ErrorMessageInvalidHost, description: "should capture whitespace-only server name"},
			{jsonData: `{ "host": "foo" }`, password: "", wantErr: ErrorMessageInvalidPort, description: "should capture nil port"},
			{jsonData: `  "host": "foo", "port": 443, "username" : "foo" }`, password: "", wantErr: ErrorMessageInvalidJSON, description: "should capture invalid json"},
		}
		for i, tc := range tests {
			t.Run(fmt.Sprintf("[%v/%v] %s", i+1, len(tests), tc.description), func(t *testing.T) {
				_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
					JSONData:                []byte(tc.jsonData),
					DecryptedSecureJSONData: map[string]string{"password": tc.password},
				})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("%s not captured. %s", tc.wantErr, err.Error())
				}
			})
		}
	})

	t.Run("should parse rowCapacityHint", func(t *testing.T) {
		ctx := context.Background()
		tests := []struct {
			description string
			jsonData    string
			want        int64
		}{
			{description: "absent defaults to 0", jsonData: `{"host": "foo", "port": 443}`, want: 0},
			{description: "numeric value", jsonData: `{"host": "foo", "port": 443, "rowCapacityHint": 50000}`, want: 50000},
			{description: "string value", jsonData: `{"host": "foo", "port": 443, "rowCapacityHint": "50000"}`, want: 50000},
			{description: "negative clamps to 0", jsonData: `{"host": "foo", "port": 443, "rowCapacityHint": -1}`, want: 0},
			{description: "invalid string defaults to 0", jsonData: `{"host": "foo", "port": 443, "rowCapacityHint": "abc"}`, want: 0},
		}
		for _, tc := range tests {
			t.Run(tc.description, func(t *testing.T) {
				got, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
					JSONData:                []byte(tc.jsonData),
					DecryptedSecureJSONData: map[string]string{},
				})
				assert.NoError(t, err)
				assert.Equal(t, tc.want, got.RowCapacityHint)
			})
		}
	})

	t.Run("should parse enforced flag on custom settings", func(t *testing.T) {
		ctx := context.Background()
		tests := []struct {
			description    string
			jsonData       string
			wantEnforced   []bool
			wantReadOnly   bool
		}{
			{
				description:  "enforced absent defaults to false",
				jsonData:     `{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "v1"}]}`,
				wantEnforced: []bool{false},
				wantReadOnly: false,
			},
			{
				description:  "enforced as bool true",
				jsonData:     `{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "v1", "enforced": true}]}`,
				wantEnforced: []bool{true},
				wantReadOnly: true,
			},
			{
				description:  "enforced as bool false",
				jsonData:     `{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "v1", "enforced": false}]}`,
				wantEnforced: []bool{false},
				wantReadOnly: false,
			},
			{
				description:  `enforced as string "true"`,
				jsonData:     `{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "v1", "enforced": "true"}]}`,
				wantEnforced: []bool{true},
				wantReadOnly: true,
			},
			{
				description:  `enforced as string "false"`,
				jsonData:     `{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "v1", "enforced": "false"}]}`,
				wantEnforced: []bool{false},
				wantReadOnly: false,
			},
			{
				description:  "mixed: only one enforced triggers EnforceReadOnly",
				jsonData:     `{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "v1", "enforced": true}, {"setting": "s2", "value": "v2"}]}`,
				wantEnforced: []bool{true, false},
				wantReadOnly: true,
			},
		}
		for _, tc := range tests {
			t.Run(tc.description, func(t *testing.T) {
				got, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
					JSONData:                []byte(tc.jsonData),
					DecryptedSecureJSONData: map[string]string{},
				})
				assert.NoError(t, err)
				assert.Equal(t, tc.wantReadOnly, got.EnforceReadOnly, "EnforceReadOnly mismatch")
				for i, wantEnforced := range tc.wantEnforced {
					assert.Equal(t, wantEnforced, got.CustomSettings[i].Enforced, "Enforced mismatch at index %d", i)
				}
			})
		}
	})

	t.Run("should parse enforceReadOnly at top level", func(t *testing.T) {
		ctx := context.Background()
		tests := []struct {
			description  string
			jsonData     string
			wantReadOnly bool
		}{
			{
				description:  "absent defaults to false",
				jsonData:     `{"host": "foo", "port": 443}`,
				wantReadOnly: false,
			},
			{
				description:  "bool true",
				jsonData:     `{"host": "foo", "port": 443, "enforceReadOnly": true}`,
				wantReadOnly: true,
			},
			{
				description:  "bool false",
				jsonData:     `{"host": "foo", "port": 443, "enforceReadOnly": false}`,
				wantReadOnly: false,
			},
			{
				description:  `string "true"`,
				jsonData:     `{"host": "foo", "port": 443, "enforceReadOnly": "true"}`,
				wantReadOnly: true,
			},
			{
				description:  `string "false"`,
				jsonData:     `{"host": "foo", "port": 443, "enforceReadOnly": "false"}`,
				wantReadOnly: false,
			},
			{
				description:  "enforceReadOnly false + enforced setting true => auto-true",
				jsonData:     `{"host": "foo", "port": 443, "enforceReadOnly": false, "customSettings": [{"setting": "s1", "value": "v1", "enforced": true}]}`,
				wantReadOnly: true,
			},
		}
		for _, tc := range tests {
			t.Run(tc.description, func(t *testing.T) {
				got, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
					JSONData:                []byte(tc.jsonData),
					DecryptedSecureJSONData: map[string]string{},
				})
				assert.NoError(t, err)
				assert.Equal(t, tc.wantReadOnly, got.EnforceReadOnly, "EnforceReadOnly mismatch")
			})
		}
	})

	t.Run("header source custom settings", func(t *testing.T) {
		ctx := context.Background()

		t.Run("valid header source parses and validates", func(t *testing.T) {
			got, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "header", "headerName": "X-Allowed-Projects"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.NoError(t, err)
			assert.True(t, got.EnforceReadOnly)
			assert.Equal(t, "header", got.CustomSettings[0].Source)
			assert.Equal(t, "X-Allowed-Projects", got.CustomSettings[0].HeaderName)
		})

		t.Run("header source without headerName is rejected", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "header"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.True(t, errors.Is(err, backend.DownstreamError(err)), "expected downstream error")
		})

		t.Run("header source with value set is rejected", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "foo", "enforced": true, "source": "header", "headerName": "X-Hdr"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
		})

		t.Run("header source with enforced=false is rejected", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "source": "header", "headerName": "X-Hdr"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
		})

		t.Run("header source binding to readonly is rejected", func(t *testing.T) {
			for _, name := range []string{"readonly", "READONLY", "ReadOnly"} {
				name := name
				t.Run(name, func(t *testing.T) {
					jsonData := fmt.Sprintf(`{"host": "foo", "port": 443, "customSettings": [{"setting": %q, "enforced": true, "source": "header", "headerName": "X-Hdr"}]}`, name)
					_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
						JSONData:                []byte(jsonData),
						DecryptedSecureJSONData: map[string]string{},
					})
					assert.Error(t, err)
				})
			}
		})

		t.Run("header source onMissing accept reject and empty", func(t *testing.T) {
			for _, om := range []string{"reject", "empty"} {
				om := om
				t.Run(om, func(t *testing.T) {
					jsonData := fmt.Sprintf(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "header", "headerName": "X-Hdr", "onMissing": %q}]}`, om)
					got, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
						JSONData:                []byte(jsonData),
						DecryptedSecureJSONData: map[string]string{},
					})
					assert.NoError(t, err)
					assert.Equal(t, om, got.CustomSettings[0].OnMissing)
				})
			}
		})

		t.Run("header source unknown onMissing is rejected", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "header", "headerName": "X-Hdr", "onMissing": "fallback"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
		})

		t.Run("headerName is canonicalised in binding", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "header", "headerName": "x-allowed-projects"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.NoError(t, err)
			// Verify the canonical form through BuildEnforcedBinding directly.
			cs := CustomSetting{Setting: "s1", Enforced: true, Source: "header", HeaderName: "x-allowed-projects"}
			b, err := BuildEnforcedBinding(cs, EnforcedSourceRuntime{})
			assert.NoError(t, err)
			hs, ok := b.Source.(headerValueSource)
			assert.True(t, ok)
			assert.Equal(t, "X-Allowed-Projects", hs.headerName)
		})

		t.Run("unknown source is rejected", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "value": "v", "enforced": true, "source": "magic"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
		})

		t.Run("header-sourced row excluded from enforcedSettings static map", func(t *testing.T) {
			got, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "header", "headerName": "X-Hdr"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.NoError(t, err)
			assert.Nil(t, got.enforcedSettings(), "dynamic-source entries must be excluded from static map")
			assert.True(t, got.shouldForceReadOnly(), "readonly must still be forced")
			bindings, err := got.enforcedBindings()
			assert.NoError(t, err)
			assert.Len(t, bindings, 1)
		})
	})

	t.Run("jwt source custom settings", func(t *testing.T) {
		ctx := context.Background()

		// Happy-path and normalization tests exercise validateAndNormalizeJWTCustomSetting
		// directly for conciseness. LoadSettings also accepts valid jwt rows now that
		// the JWT factory is registered (commit 2).

		t.Run("happy path: defaults filled in and canonicalized", func(t *testing.T) {
			cs := CustomSetting{
				Setting:  "tenant",
				Enforced: true,
				Source:   CustomSettingSourceJWT,
				JWTClaim: "tenants",
			}
			err := validateAndNormalizeJWTCustomSetting(&cs)
			assert.NoError(t, err)
			assert.Equal(t, "X-Grafana-Id", cs.JWTHeaderName, "empty jwtHeaderName should default to X-Grafana-Id")
			assert.Equal(t, ",", cs.JWTClaimJoin, "empty jwtClaimJoin should default to ','")
			assert.Equal(t, CustomSettingJWTVerifyNone, cs.JWTVerify, "empty jwtVerify should default to 'none'")
		})

		t.Run("happy path: jwtHeaderName is canonicalized", func(t *testing.T) {
			cs := CustomSetting{
				Setting:       "tenant",
				Enforced:      true,
				Source:        CustomSettingSourceJWT,
				JWTClaim:      "tenants",
				JWTHeaderName: "authorization",
			}
			err := validateAndNormalizeJWTCustomSetting(&cs)
			assert.NoError(t, err)
			assert.Equal(t, "Authorization", cs.JWTHeaderName)
		})

		t.Run("happy path: nested claim a.b.c, custom join |, verify none", func(t *testing.T) {
			cs := CustomSetting{
				Setting:      "tenant",
				Enforced:     true,
				Source:       CustomSettingSourceJWT,
				JWTClaim:     "a.b.c",
				JWTClaimJoin: "|",
				JWTVerify:    "none",
			}
			err := validateAndNormalizeJWTCustomSetting(&cs)
			assert.NoError(t, err)
			assert.Equal(t, "|", cs.JWTClaimJoin)
		})

		t.Run("happy path: verify=jwks with valid URL, issuer, audience", func(t *testing.T) {
			cs := CustomSetting{
				Setting:    "tenant",
				Enforced:   true,
				Source:     CustomSettingSourceJWT,
				JWTClaim:   "tenants",
				JWTVerify:  "jwks",
				JWTJWKSURL: "https://issuer.example/keys",
				JWTIssuer:  "x",
				JWTAudience: "y",
			}
			err := validateAndNormalizeJWTCustomSetting(&cs)
			assert.NoError(t, err)
		})

		// Rejection tests go through LoadSettings so the full parsing → validation
		// pipeline is exercised. Each should fail before reaching enforcedBindings.

		t.Run("rejection: source=jwt without enforced=true", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "source": "jwt", "jwtClaim": "tenants"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "enforced")
		})

		t.Run("rejection: source=jwt with value set", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt", "jwtClaim": "tenants", "value": "fallback"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "value")
		})

		t.Run("rejection: source=jwt targeting readonly", func(t *testing.T) {
			for _, name := range []string{"readonly", "READONLY", "ReadOnly"} {
				name := name
				t.Run(name, func(t *testing.T) {
					jsonData := fmt.Sprintf(`{"host": "foo", "port": 443, "customSettings": [{"setting": %q, "enforced": true, "source": "jwt", "jwtClaim": "tenants"}]}`, name)
					_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
						JSONData:                []byte(jsonData),
						DecryptedSecureJSONData: map[string]string{},
					})
					assert.Error(t, err)
					assert.Contains(t, err.Error(), "reserved")
				})
			}
		})

		t.Run("rejection: jwtClaim empty", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "jwtClaim")
		})

		t.Run("rejection: jwtClaim containing ..", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt", "jwtClaim": "a..b"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "consecutive dots")
		})

		t.Run("rejection: unknown jwtVerify", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt", "jwtClaim": "tenants", "jwtVerify": "magic"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "magic")
		})

		t.Run("rejection: verify=jwks with empty URL", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt", "jwtClaim": "tenants", "jwtVerify": "jwks"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "jwtJwksUrl")
		})

		t.Run("rejection: verify=jwks with http URL", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt", "jwtClaim": "tenants", "jwtVerify": "jwks", "jwtJwksUrl": "http://issuer.example/keys"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "https")
		})

		t.Run("rejection: verify=jwks with unparseable URL", func(t *testing.T) {
			// \x00 (null byte) makes url.Parse return an error.
			cs := CustomSetting{
				Setting:    "s1",
				Enforced:   true,
				Source:     CustomSettingSourceJWT,
				JWTClaim:   "tenants",
				JWTVerify:  "jwks",
				JWTJWKSURL: "https://issuer.example/\x00keys",
			}
			err := validateAndNormalizeJWTCustomSetting(&cs)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "jwtJwksUrl")
		})

		t.Run("rejection: verify=none with jwtJwksUrl set", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt", "jwtClaim": "tenants", "jwtJwksUrl": "https://issuer.example/keys"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "jwtJwksUrl")
			assert.Contains(t, err.Error(), "none")
		})

		t.Run("rejection: verify=none with jwtIssuer set", func(t *testing.T) {
			_, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
				JSONData:                []byte(`{"host": "foo", "port": 443, "customSettings": [{"setting": "s1", "enforced": true, "source": "jwt", "jwtClaim": "tenants", "jwtIssuer": "issuer.example"}]}`),
				DecryptedSecureJSONData: map[string]string{},
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "jwtIssuer")
			assert.Contains(t, err.Error(), "none")
		})

		t.Run("rejection: whitespace-padded jwtIssuer", func(t *testing.T) {
			cs := CustomSetting{
				Setting:    "s1",
				Enforced:   true,
				Source:     CustomSettingSourceJWT,
				JWTClaim:   "tenants",
				JWTVerify:  "jwks",
				JWTJWKSURL: "https://issuer.example/keys",
				JWTIssuer:  " issuer.example ",
			}
			err := validateAndNormalizeJWTCustomSetting(&cs)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "whitespace")
		})

		t.Run("defaulting: empty header, join, verify", func(t *testing.T) {
			cs := CustomSetting{
				Setting:  "tenant",
				Enforced: true,
				Source:   CustomSettingSourceJWT,
				JWTClaim: "tenants",
				// JWTHeaderName, JWTClaimJoin, JWTVerify all empty → should be defaulted
			}
			err := validateAndNormalizeJWTCustomSetting(&cs)
			assert.NoError(t, err)
			assert.Equal(t, "X-Grafana-Id", cs.JWTHeaderName)
			assert.Equal(t, ",", cs.JWTClaimJoin)
			assert.Equal(t, "none", cs.JWTVerify)
		})

		// With the JWT factory registered, enforcedBindings succeeds for well-formed jwt rows.
		// Verify the binding has the right kind and the claim path is stored.
		t.Run("enforcedBindings: succeeds for valid jwt verify=none row", func(t *testing.T) {
			s := Settings{
				CustomSettings: []CustomSetting{
					{
						Setting:       "tenant",
						Enforced:      true,
						Source:        CustomSettingSourceJWT,
						JWTClaim:      "tenants",
						JWTHeaderName: "X-Grafana-Id",
						JWTClaimJoin:  ",",
						JWTVerify:     "none",
					},
				},
			}
			bindings, err := s.enforcedBindings()
			assert.NoError(t, err)
			if assert.Len(t, bindings, 1) {
				assert.Equal(t, CustomSettingSourceJWT, bindings[0].Source.Kind(), "source kind should be jwt")
				assert.Equal(t, "tenant", bindings[0].Setting, "setting name should be preserved")
			}
		})

		// JWT rows count as enforced, so shouldForceReadOnly must return true.
		t.Run("jwt-sourced row forces readonly", func(t *testing.T) {
			s := Settings{
				CustomSettings: []CustomSetting{
					{
						Setting:       "tenant",
						Enforced:      true,
						Source:        CustomSettingSourceJWT,
						JWTClaim:      "tenants",
						JWTHeaderName: "X-Grafana-Id",
						JWTClaimJoin:  ",",
						JWTVerify:     "none",
					},
				},
			}
			assert.True(t, s.shouldForceReadOnly())
			assert.Nil(t, s.enforcedSettings(), "dynamic-source entries must be excluded from static map")
		})
	})
}

func TestLoadSettingsOAuthPassThru(t *testing.T) {
	ctx := context.Background()

	t.Run("should parse oauthPassThru as bool", func(t *testing.T) {
		settings, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
			JSONData:                []byte(`{"host": "test", "port": 443, "oauthPassThru": true}`),
			DecryptedSecureJSONData: map[string]string{},
		})
		assert.NoError(t, err)
		assert.True(t, settings.OAuthPassThru)
	})

	t.Run("should parse oauthPassThru as string", func(t *testing.T) {
		settings, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
			JSONData:                []byte(`{"host": "test", "port": 443, "oauthPassThru": "true"}`),
			DecryptedSecureJSONData: map[string]string{},
		})
		assert.NoError(t, err)
		assert.True(t, settings.OAuthPassThru)
	})

	t.Run("should default oauthPassThru to false", func(t *testing.T) {
		settings, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
			JSONData:                []byte(`{"host": "test", "port": 443}`),
			DecryptedSecureJSONData: map[string]string{},
		})
		assert.NoError(t, err)
		assert.False(t, settings.OAuthPassThru)
	})
}

func TestLoadSettingsOAuthPassThruAllowFallback(t *testing.T) {
	ctx := context.Background()

	t.Run("should parse oauthPassThruAllowFallback as bool", func(t *testing.T) {
		settings, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
			JSONData:                []byte(`{"host": "test", "port": 443, "oauthPassThruAllowFallback": true}`),
			DecryptedSecureJSONData: map[string]string{},
		})
		assert.NoError(t, err)
		assert.True(t, settings.OAuthPassThruAllowFallback)
	})

	t.Run("should parse oauthPassThruAllowFallback as string", func(t *testing.T) {
		settings, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
			JSONData:                []byte(`{"host": "test", "port": 443, "oauthPassThruAllowFallback": "true"}`),
			DecryptedSecureJSONData: map[string]string{},
		})
		assert.NoError(t, err)
		assert.True(t, settings.OAuthPassThruAllowFallback)
	})

	t.Run("should default oauthPassThruAllowFallback to false", func(t *testing.T) {
		settings, err := LoadSettings(ctx, backend.DataSourceInstanceSettings{
			JSONData:                []byte(`{"host": "test", "port": 443}`),
			DecryptedSecureJSONData: map[string]string{},
		})
		assert.NoError(t, err)
		assert.False(t, settings.OAuthPassThruAllowFallback)
	})
}
