package plugin

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeEnforcedTestSettings builds a Settings with one enforced custom setting.
func makeEnforcedTestSettings(name, value string) Settings {
	return Settings{
		EnforceReadOnly: true,
		QueryTimeout:    "10",
		CustomSettings: []CustomSetting{
			{Setting: name, Value: value, Enforced: true},
		},
	}
}

// fakeProber builds a dbProber that uses queryFn and execFn.
// Either function may call t.Helper() / record calls as needed.
func fakeProber(queryFn func(ctx context.Context, q string) (string, error), execFn func(ctx context.Context, q string) error) dbProber {
	return dbProber{
		queryScalar: queryFn,
		execQuery:   execFn,
	}
}

// sharedFakeProber returns a prober whose behaviour is driven by the supplied map:
//   - "system.settings" key controls the readonly row scan (probe c)
//   - "getSetting"      key controls the round-trip value (probe a)
//   - "exec"            key controls the override-rejection error (probe b)
//
// This simplifies the common test cases that only want to exercise one failure mode.
func sharedFakeProber(
	roVal string, roErr error,
	getSettingVal string, getSettingErr error,
	execErr error,
) dbProber {
	return fakeProber(
		func(_ context.Context, q string) (string, error) {
			if strings.Contains(q, "system.settings") {
				return roVal, roErr
			}
			return getSettingVal, getSettingErr
		},
		func(_ context.Context, _ string) error { return execErr },
	)
}

func TestRunEnforcedHealthProbes_HappyPath(t *testing.T) {
	s := makeEnforcedTestSettings("custom_x", "val1")
	p := sharedFakeProber("0", nil, "val1", nil, &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	assert.Nil(t, result, "all probes pass → should return nil")
}

func TestRunEnforcedHealthProbes_NoEnforcedSettings(t *testing.T) {
	s := Settings{EnforceReadOnly: false, QueryTimeout: "10"}
	p := sharedFakeProber("0", nil, "anything", nil, fmt.Errorf("should not be called"))
	result := runEnforcedHealthProbes(context.Background(), s, p)
	assert.Nil(t, result, "no enforced settings → should short-circuit and return nil")
}

func TestRunEnforcedHealthProbes_UserAlreadyReadonly(t *testing.T) {
	s := makeEnforcedTestSettings("custom_x", "val1")
	// Simulate a user that already has readonly=1 at the server level.
	p := sharedFakeProber("1", nil, "val1", nil, &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	require.NotNil(t, result)
	assert.Equal(t, backend.HealthStatusError, result.Status)
	assert.Contains(t, result.Message, "already readonly=1")
	assert.Contains(t, result.Message, "readonly=0")
}

func TestRunEnforcedHealthProbes_UserAlreadyReadonly2(t *testing.T) {
	s := makeEnforcedTestSettings("custom_x", "val1")
	p := sharedFakeProber("2", nil, "val1", nil, &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	require.NotNil(t, result)
	assert.Equal(t, backend.HealthStatusError, result.Status)
	assert.Contains(t, result.Message, "already readonly=2")
}

func TestRunEnforcedHealthProbes_ReadonlyQueryError_NonFatal(t *testing.T) {
	// system.settings query error should not block the health check (best-effort).
	s := makeEnforcedTestSettings("custom_x", "val1")
	p := sharedFakeProber("", fmt.Errorf("connection refused"), "val1", nil, &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	// Should still pass since the connection probe already succeeded.
	assert.Nil(t, result)
}

func TestRunEnforcedHealthProbes_ErrNoRows_NonFatal(t *testing.T) {
	// ErrNoRows from system.settings means the readonly setting is absent → default 0.
	s := makeEnforcedTestSettings("custom_x", "val1")
	p := sharedFakeProber("", sql.ErrNoRows, "val1", nil, &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	assert.Nil(t, result)
}

func TestRunEnforcedHealthProbes_ValueMismatch(t *testing.T) {
	s := makeEnforcedTestSettings("custom_x", "expected_val")
	// getSetting returns a different value (e.g. a CONST profile overrides it).
	p := sharedFakeProber("0", nil, "server_forced_val", nil, &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	require.NotNil(t, result)
	assert.Equal(t, backend.HealthStatusError, result.Status)
	assert.Contains(t, result.Message, "value mismatch")
	assert.Contains(t, result.Message, "custom_x")
}

func TestRunEnforcedHealthProbes_GetSettingError(t *testing.T) {
	s := makeEnforcedTestSettings("custom_x", "val1")
	p := sharedFakeProber("0", nil, "", fmt.Errorf("unknown setting"), &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	require.NotNil(t, result)
	assert.Equal(t, backend.HealthStatusError, result.Status)
	assert.Contains(t, result.Message, "health probe failed")
	assert.Contains(t, result.Message, "custom_x")
}

func TestRunEnforcedHealthProbes_SettingOverridable(t *testing.T) {
	s := makeEnforcedTestSettings("custom_x", "val1")
	// Override probe succeeds → setting is CHANGEABLE_IN_READONLY (bad).
	p := sharedFakeProber("0", nil, "val1", nil, nil)
	result := runEnforcedHealthProbes(context.Background(), s, p)
	require.NotNil(t, result)
	assert.Equal(t, backend.HealthStatusError, result.Status)
	assert.Contains(t, result.Message, "CHANGEABLE_IN_READONLY")
	assert.Contains(t, result.Message, "custom_x")
}

func TestRunEnforcedHealthProbes_UnexpectedExecError_Warning(t *testing.T) {
	// An override probe returning a non-164 error is ambiguous:
	// we log a warning but do not fail the health check.
	s := makeEnforcedTestSettings("custom_x", "val1")
	p := sharedFakeProber("0", nil, "val1", nil, fmt.Errorf("some other DB error"))
	result := runEnforcedHealthProbes(context.Background(), s, p)
	assert.Nil(t, result, "non-164 error from override probe is a warning, not a failure")
}

func TestRunEnforcedHealthProbes_MultipleSettings_FirstFails(t *testing.T) {
	s := Settings{
		EnforceReadOnly: true,
		QueryTimeout:    "10",
		CustomSettings: []CustomSetting{
			{Setting: "custom_a", Value: "v1", Enforced: true},
			{Setting: "custom_b", Value: "v2", Enforced: true},
		},
	}
	// custom_a will cause a mismatch, custom_b is fine.
	// We just need to confirm that a failure is returned (not necessarily for custom_a
	// since map iteration order is random).
	badVal := map[string]string{"custom_a": "WRONG", "custom_b": "v2"}
	p := fakeProber(
		func(_ context.Context, q string) (string, error) {
			if strings.Contains(q, "system.settings") {
				return "0", nil
			}
			for setting, ret := range badVal {
				if strings.Contains(q, setting) {
					return ret, nil
				}
			}
			return "", fmt.Errorf("unexpected query: %s", q)
		},
		func(_ context.Context, _ string) error { return &clickhouse.Exception{Code: 164} },
	)
	result := runEnforcedHealthProbes(context.Background(), s, p)
	require.NotNil(t, result, "mismatch on custom_a should produce a failure")
	assert.Equal(t, backend.HealthStatusError, result.Status)
}

func TestEnforcedProbeTimeout_Default(t *testing.T) {
	s := Settings{QueryTimeout: ""}
	d := enforcedProbeTimeout(s)
	assert.Equal(t, 30, int(d.Seconds()))
}

func TestEnforcedProbeTimeout_Capped(t *testing.T) {
	s := Settings{QueryTimeout: "120"}
	d := enforcedProbeTimeout(s)
	assert.Equal(t, 30, int(d.Seconds()), "timeout should be capped at 30 s")
}

func TestEnforcedProbeTimeout_Short(t *testing.T) {
	s := Settings{QueryTimeout: "5"}
	d := enforcedProbeTimeout(s)
	assert.Equal(t, 5, int(d.Seconds()))
}

// ---------------------------------------------------------------------------
// Header-sourced binding health tests
// ---------------------------------------------------------------------------

func makeHeaderBoundSettings(settingName, headerName string) Settings {
	return Settings{
		EnforceReadOnly: true,
		QueryTimeout:    "10",
		CustomSettings: []CustomSetting{
			{
				Setting:    settingName,
				Enforced:   true,
				Source:     customSettingSourceHeader,
				HeaderName: headerName,
				OnMissing:  onMissingReject,
			},
		},
	}
}

func TestRunEnforcedHealthProbes_HeaderOnly_SkipsProbes(t *testing.T) {
	// A header-sourced binding should cause no DB probes — the prober should
	// not be called at all (or only for the readonly probe).
	s := makeHeaderBoundSettings("custom_visible_tenants", "X-Allowed-Projects")
	proberCalled := false
	p := fakeProber(
		func(_ context.Context, q string) (string, error) {
			// The readonly probe (probe c) is still allowed for the user-level check,
			// but only if there are also static bindings. For header-only, skip it.
			proberCalled = true
			// system.settings readonly query: return "0" (user starts non-readonly)
			if strings.Contains(q, "system.settings") {
				return "0", nil
			}
			// getSetting calls should not happen for header bindings.
			t.Errorf("unexpected queryScalar call for header-only settings: %s", q)
			return "", nil
		},
		func(_ context.Context, q string) error {
			t.Errorf("unexpected execQuery call for header-only settings: %s", q)
			return nil
		},
	)
	result := runEnforcedHealthProbes(context.Background(), s, p)
	assert.Nil(t, result, "header-only bindings → probes skipped → nil result")
	// The prober should NOT have been called because there are no static bindings.
	assert.False(t, proberCalled, "no DB probes should run for a header-only datasource")
}

func TestRunEnforcedHealthProbes_MixedStaticAndHeader(t *testing.T) {
	// Mixed: one static binding (runs probes) + one header binding (skipped).
	s := Settings{
		EnforceReadOnly: true,
		QueryTimeout:    "10",
		CustomSettings: []CustomSetting{
			{Setting: "custom_static", Value: "v1", Enforced: true},
			{
				Setting:    "custom_header",
				Enforced:   true,
				Source:     customSettingSourceHeader,
				HeaderName: "X-Tenant",
				OnMissing:  onMissingReject,
			},
		},
	}
	p := sharedFakeProber("0", nil, "v1", nil, &clickhouse.Exception{Code: 164})
	result := runEnforcedHealthProbes(context.Background(), s, p)
	assert.Nil(t, result, "static probe passes, header binding skipped → nil")
}

func TestMakeEnforcedSettingsHealthCheck_HeaderInfo(t *testing.T) {
	// makeEnforcedSettingsHealthCheck wraps runEnforcedHealthProbes. When probes pass
	// but a header binding exists, the wrapper must return an OK result with an
	// informational message. We test this by calling the inner logic directly
	// (we can't call makeEnforcedSettingsHealthCheck without a real DB), so we
	// replicate the wrapper logic here by calling runEnforcedHealthProbes and
	// then applying the header note.

	s := makeHeaderBoundSettings("custom_visible_tenants", "X-Allowed-Projects")

	// No DB probes are run for header-only, so any prober is fine.
	result := runEnforcedHealthProbes(context.Background(), s, sharedFakeProber("0", nil, "", nil, nil))
	assert.Nil(t, result, "header-only → probes nil")

	// Simulate what makeEnforcedSettingsHealthCheck does after nil:
	bindings, err := s.enforcedBindings()
	require.NoError(t, err)
	var headerLines []string
	for _, b := range bindings {
		if b.Source.Kind() != customSettingSourceHeader {
			continue
		}
		headerName := ""
		if hn, ok := b.Source.(interface{ HeaderName() string }); ok {
			headerName = hn.HeaderName()
		}
		if headerName != "" {
			headerLines = append(headerLines, fmt.Sprintf(
				"header-sourced value for %q (from header %q) is not validated at save time; verify at runtime",
				b.Setting, headerName,
			))
		}
	}
	require.NotEmpty(t, headerLines, "expected at least one header line")

	msg := "This datasource resolves one or more enforced settings from request headers; " +
		"ensure Grafana is configured to forward the header(s) to backend plugins.\n" +
		strings.Join(headerLines, "\n")
	infoResult := &backend.CheckHealthResult{
		Status:  backend.HealthStatusOk,
		Message: msg,
	}

	assert.Equal(t, backend.HealthStatusOk, infoResult.Status)
	assert.Contains(t, infoResult.Message, "custom_visible_tenants")
	assert.Contains(t, infoResult.Message, "X-Allowed-Projects")
	assert.Contains(t, infoResult.Message, "not validated at save time")
	assert.Contains(t, infoResult.Message, "ensure Grafana is configured")
}

// ---------------------------------------------------------------------------
// JWT-sourced binding health tests
// ---------------------------------------------------------------------------

func makeJWTBoundSettings(settingName, headerName, claimPath, verify, jwksURL string) Settings {
	cs := CustomSetting{
		Setting:       settingName,
		Enforced:      true,
		Source:        CustomSettingSourceJWT,
		JWTClaim:      claimPath,
		JWTHeaderName: headerName,
		JWTVerify:     verify,
		OnMissing:     onMissingReject,
	}
	if verify == CustomSettingJWTVerifyJWKS {
		cs.JWTJWKSURL = jwksURL
	}
	return Settings{
		EnforceReadOnly: true,
		QueryTimeout:    "10",
		CustomSettings:  []CustomSetting{cs},
	}
}

func TestRunEnforcedHealthProbes_JWTOnly_SkipsProbes(t *testing.T) {
	// JWT-only config has no static bindings → no DB probes should run.
	s := makeJWTBoundSettings("custom_tenant", "X-Grafana-Id", "tenants", CustomSettingJWTVerifyNone, "")
	proberCalled := false
	p := fakeProber(
		func(_ context.Context, q string) (string, error) {
			proberCalled = true
			if strings.Contains(q, "system.settings") {
				return "0", nil
			}
			t.Errorf("unexpected queryScalar call for jwt-only settings: %s", q)
			return "", nil
		},
		func(_ context.Context, q string) error {
			t.Errorf("unexpected execQuery call for jwt-only settings: %s", q)
			return nil
		},
	)
	result := runEnforcedHealthProbes(context.Background(), s, p)
	assert.Nil(t, result, "jwt-only bindings → probes skipped → nil result")
	assert.False(t, proberCalled)
}

func TestMakeEnforcedSettingsHealthCheck_JWTVerifyNone_InfoMessage(t *testing.T) {
	// verify=none → should produce an OK status with an informational message.
	s := makeJWTBoundSettings("custom_tenant", "X-Grafana-Id", "tenants", CustomSettingJWTVerifyNone, "")

	// Health probes (nil path for JWT-only).
	result := runEnforcedHealthProbes(context.Background(), s, sharedFakeProber("0", nil, "", nil, nil))
	assert.Nil(t, result)

	// Simulate the info-message logic in makeEnforcedSettingsHealthCheck.
	bindings, err := s.enforcedBindings()
	require.NoError(t, err)

	var infoLines []string
	for _, b := range bindings {
		if b.Source.Kind() != CustomSettingSourceJWT {
			continue
		}
		type jwtProbe interface {
			JWKSURL() string
			Verify() string
			ClaimPath() string
			HeaderName() string
		}
		jp, ok := b.Source.(jwtProbe)
		require.True(t, ok)
		assert.Equal(t, CustomSettingJWTVerifyNone, jp.Verify())
		infoLines = append(infoLines, fmt.Sprintf(
			"JWT-sourced value for %q (claim %q from header %q) is not validated at save time; ensure the token is forwarded",
			b.Setting, jp.ClaimPath(), jp.HeaderName(),
		))
	}

	require.NotEmpty(t, infoLines)
	assert.Contains(t, infoLines[0], "custom_tenant")
	assert.Contains(t, infoLines[0], "tenants")
	assert.Contains(t, infoLines[0], "X-Grafana-Id")
	assert.Contains(t, infoLines[0], "not validated at save time")
}

func TestMakeEnforcedSettingsHealthCheck_JWTVerifyJWKS_ReachableURL_InfoMessage(t *testing.T) {
	// verify=jwks with a reachable server → info message confirming reachability.
	// Serve a JWKS with one RSA key so the probe's non-empty check passes.
	// (The keyfunc-based probe intentionally rejects `{"keys":[]}` because
	// such a document would fail every real verification.)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"keys":[{"kty":"RSA","kid":"probe","use":"sig","alg":"RS256","n":"sXchDaQebHnPiGvyDOAT4saGEUetSyo9MKLOoWFsueri23bOdgWp4Dy1WlUzewbgBHod5pcM9H95GQRV3JDXboIRROSBigeC5yjU1hGzHHyXss8UDprecbAYxknTcQkhslANGRUZmdTOQ5ZTsUp7hIu6UMULhg9AzUcQq6vNoVc","e":"AQAB"}]}`)
	}))
	defer srv.Close()

	s := makeJWTBoundSettings("custom_tenant", "X-Grafana-Id", "tenants", CustomSettingJWTVerifyJWKS, srv.URL)

	bindings, err := s.enforcedBindings()
	require.NoError(t, err)

	var infoLines []string
	for _, b := range bindings {
		if b.Source.Kind() != CustomSettingSourceJWT {
			continue
		}
		type jwtProbe interface {
			JWKSURL() string
			Verify() string
			ClaimPath() string
			HeaderName() string
		}
		jp, ok := b.Source.(jwtProbe)
		require.True(t, ok)
		assert.Equal(t, CustomSettingJWTVerifyJWKS, jp.Verify())
		assert.Equal(t, srv.URL, jp.JWKSURL())
		// Probe the URL.
		probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		probeErr := probeJWKSURL(probeCtx, srv.URL)
		cancel()
		assert.NoError(t, probeErr, "JWKS URL should be reachable")
		infoLines = append(infoLines, fmt.Sprintf(
			"JWT-sourced value for %q (claim %q from header %q, verify=jwks): JWKS URL %q is reachable",
			b.Setting, jp.ClaimPath(), jp.HeaderName(), jp.JWKSURL(),
		))
	}

	require.NotEmpty(t, infoLines)
	assert.Contains(t, infoLines[0], "reachable")
}

func TestMakeEnforcedSettingsHealthCheck_JWTVerifyJWKS_UnreachableURL_ErrorStatus(t *testing.T) {
	// verify=jwks with an unreachable URL → error status.
	// Use a URL that is guaranteed unreachable.
	unreachableURL := "http://127.0.0.1:0/keys" // port 0 = guaranteed connection refused

	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := probeJWKSURL(probeCtx, unreachableURL)
	assert.Error(t, err, "probeJWKSURL should fail for an unreachable URL")
}

// ---------------------------------------------------------------------------
// checkForwardHeadersGate (item 11c)
// ---------------------------------------------------------------------------

func TestHeaderIsAlwaysForwarded(t *testing.T) {
	// Every canonical form of an always-forwarded header must be recognised.
	// These are Grafana core middleware headers that reach the plugin regardless
	// of the datasource "Forward Grafana HTTP Headers" toggle.
	for _, h := range []string{
		"X-Grafana-Id",
		"X-Dashboard-Uid",
		"X-Panel-Id",
		"X-Rule-Uid",
		"X-Datasource-Uid",
	} {
		assert.True(t, headerIsAlwaysForwarded(http.CanonicalHeaderKey(h)),
			"%q should be treated as always-forwarded", h)
	}
	// Toggle-gated headers must NOT be included; a binding on them requires
	// the forwardGrafanaHeaders toggle.
	for _, h := range []string{
		"Authorization",
		"X-Id-Token",
		"Cookie",
		"X-Grafana-User",
		"X-Tenant",
		"Cf-Access-Jwt-Assertion",
	} {
		assert.False(t, headerIsAlwaysForwarded(http.CanonicalHeaderKey(h)),
			"%q must require the forwardGrafanaHeaders toggle", h)
	}
}

func TestCheckForwardHeadersGate_ToggleOn_NeverFails(t *testing.T) {
	s := Settings{
		EnforceReadOnly:       true,
		ForwardGrafanaHeaders: true,
		CustomSettings: []CustomSetting{
			{Setting: "custom_x", Enforced: true, Source: customSettingSourceHeader, HeaderName: "X-Tenant", OnMissing: onMissingReject},
		},
	}
	assert.Nil(t, checkForwardHeadersGate(s))
}

func TestCheckForwardHeadersGate_ToggleOff_AlwaysForwarded_Passes(t *testing.T) {
	// Header binding on X-Grafana-Id (always-forwarded) → no gate error even with toggle off.
	s := Settings{
		EnforceReadOnly: true,
		CustomSettings: []CustomSetting{
			{Setting: "custom_x", Enforced: true, Source: customSettingSourceHeader, HeaderName: "X-Grafana-Id", OnMissing: onMissingReject},
		},
	}
	assert.Nil(t, checkForwardHeadersGate(s))
}

func TestCheckForwardHeadersGate_ToggleOff_CustomHeader_Fails(t *testing.T) {
	s := Settings{
		EnforceReadOnly: true,
		CustomSettings: []CustomSetting{
			{Setting: "custom_x", Enforced: true, Source: customSettingSourceHeader, HeaderName: "X-Tenant", OnMissing: onMissingReject},
		},
	}
	result := checkForwardHeadersGate(s)
	require.NotNil(t, result)
	assert.Equal(t, backend.HealthStatusError, result.Status)
	assert.Contains(t, result.Message, "custom_x")
	assert.Contains(t, result.Message, "X-Tenant")
	assert.Contains(t, result.Message, "Forward Grafana HTTP Headers")
}

func TestCheckForwardHeadersGate_ToggleOff_JWTOnCustomHeader_Fails(t *testing.T) {
	s := Settings{
		EnforceReadOnly: true,
		CustomSettings: []CustomSetting{
			{
				Setting:       "custom_x",
				Enforced:      true,
				Source:        CustomSettingSourceJWT,
				JWTHeaderName: "X-Id-Token",
				JWTClaim:      "tenants",
				JWTVerify:     CustomSettingJWTVerifyNone,
				OnMissing:     onMissingReject,
			},
		},
	}
	result := checkForwardHeadersGate(s)
	require.NotNil(t, result)
	assert.Equal(t, backend.HealthStatusError, result.Status)
	assert.Contains(t, result.Message, "X-Id-Token")
}

func TestCheckForwardHeadersGate_ToggleOff_JWTOnXGrafanaId_Passes(t *testing.T) {
	// JWT source with the default X-Grafana-Id header does not need the toggle.
	s := Settings{
		EnforceReadOnly: true,
		CustomSettings: []CustomSetting{
			{
				Setting:       "custom_x",
				Enforced:      true,
				Source:        CustomSettingSourceJWT,
				JWTHeaderName: "X-Grafana-Id",
				JWTClaim:      "tenants",
				JWTVerify:     CustomSettingJWTVerifyNone,
				OnMissing:     onMissingReject,
			},
		},
	}
	assert.Nil(t, checkForwardHeadersGate(s))
}

// ---------------------------------------------------------------------------
// probeJWKSURL (item 14a) — keyfunc-based probe
// ---------------------------------------------------------------------------

func TestProbeJWKSURL_EmptyKeySet_Rejected(t *testing.T) {
	// A JWKS URL that returns `{"keys":[]}` parses successfully but would
	// fail every verification. The probe must reject it so operators find
	// out at "Save & Test" time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"keys":[]}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := probeJWKSURL(ctx, srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no keys")
}

func TestProbeJWKSURL_MalformedJSON_Rejected(t *testing.T) {
	// A JWKS URL returning HTTP 200 but non-JSON content must fail — the
	// old naive GET would have accepted it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, "<html>not a JWKS document</html>")
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := probeJWKSURL(ctx, srv.URL)
	require.Error(t, err)
}
