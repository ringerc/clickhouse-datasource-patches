package plugin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helper: build a signed JWT with given claims (HS256)
// ---------------------------------------------------------------------------

const testJWTSecret = "test-secret-for-enforced-jwt-unit-tests"

func makeTestJWT(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testJWTSecret))
	require.NoError(t, err)
	return signed
}

func makeTestJWTWithHeader(t *testing.T, claims jwt.MapClaims) string {
	return "Bearer " + makeTestJWT(t, claims)
}

// jwtCtx returns a context carrying the given token in the named header.
func jwtCtx(headerName, token string) context.Context {
	return WithForwardedHeaders(context.Background(), map[string]string{
		headerName: token,
	})
}

// ---------------------------------------------------------------------------
// BuildEnforcedBinding for jwt source
// ---------------------------------------------------------------------------

func TestBuildEnforcedBinding_JWT(t *testing.T) {
	t.Run("happy path verify=none", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:       "custom_tenant",
			Enforced:      true,
			Source:        CustomSettingSourceJWT,
			JWTClaimPath:  []string{"tenants"},
			JWTHeaderName: "X-Grafana-Id",
			JWTClaimJoin:  ",",
			JWTVerify:     "none",
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		assert.Equal(t, CustomSettingSourceJWT, b.Source.Kind())
		assert.Equal(t, onMissingReject, b.OnMissing)
	})

	t.Run("onMissing=empty accepted", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:      "custom_tenant",
			Enforced:     true,
			Source:       CustomSettingSourceJWT,
			JWTClaimPath: []string{"tenants"},
			JWTVerify:    "none",
			OnMissing:    onMissingEmpty,
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		assert.Equal(t, onMissingEmpty, b.OnMissing)
	})

	t.Run("JWT source without claim is rejected", func(t *testing.T) {
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:  "s1",
			Enforced: true,
			Source:   CustomSettingSourceJWT,
		}, EnforcedSourceRuntime{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "jwtClaimPath")
	})

	t.Run("JWT source with value set is rejected", func(t *testing.T) {
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:      "s1",
			Enforced:     true,
			Source:       CustomSettingSourceJWT,
			JWTClaimPath: []string{"tenants"},
			Value:        "foo",
		}, EnforcedSourceRuntime{})
		assert.Error(t, err)
	})

	t.Run("JWT source binding readonly is rejected", func(t *testing.T) {
		for _, name := range []string{"readonly", "READONLY", "ReadOnly"} {
			name := name
			t.Run(name, func(t *testing.T) {
				_, err := BuildEnforcedBinding(CustomSetting{
					Setting:      name,
					Enforced:     true,
					Source:       CustomSettingSourceJWT,
					JWTClaimPath: []string{"tenants"},
				}, EnforcedSourceRuntime{})
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "reserved")
			})
		}
	})

	t.Run("verify=jwks with nil cache: source built but cache is nil", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:      "s1",
			Enforced:     true,
			Source:       CustomSettingSourceJWT,
			JWTClaimPath: []string{"tenants"},
			JWTVerify:    "jwks",
			JWTJWKSURL:   "https://example.com/keys",
		}, EnforcedSourceRuntime{JWKSCache: nil})
		require.NoError(t, err, "factory must not fail when JWKSCache is nil")
		src, ok := b.Source.(*jwtValueSource)
		require.True(t, ok)
		assert.Nil(t, src.cache)
	})

	t.Run("headerName is canonicalised by factory", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:       "s1",
			Enforced:      true,
			Source:        CustomSettingSourceJWT,
			JWTClaimPath:  []string{"tenants"},
			JWTHeaderName: "x-grafana-id",
		}, EnforcedSourceRuntime{})
		require.NoError(t, err)
		src, ok := b.Source.(*jwtValueSource)
		require.True(t, ok)
		assert.Equal(t, "X-Grafana-Id", src.headerName)
	})
}

// ---------------------------------------------------------------------------
// jwtValueSource.Kind / HeaderName / ClaimPath / Verify / JWKSURL
// ---------------------------------------------------------------------------

func TestJWTValueSource_Accessors(t *testing.T) {
	src := &jwtValueSource{
		settingName: "s1",
		headerName:  "X-Grafana-Id",
		claimPath:   []string{"a", "b", "c"},
		verify:      CustomSettingJWTVerifyNone,
		jwksURL:     "",
	}
	assert.Equal(t, CustomSettingSourceJWT, src.Kind())
	assert.Equal(t, "X-Grafana-Id", src.HeaderName())
	assert.Equal(t, "a.b.c", src.ClaimPath())
	assert.Equal(t, CustomSettingJWTVerifyNone, src.Verify())
	assert.Equal(t, "", src.JWKSURL())
}

// ---------------------------------------------------------------------------
// jwtValueSource.Resolve — missing / absent header cases
// ---------------------------------------------------------------------------

func TestJWTValueSource_MissingHeader(t *testing.T) {
	src := &jwtValueSource{
		settingName: "tenant",
		headerName:  "X-Grafana-Id",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyNone,
	}

	t.Run("nil forwarded headers map", func(t *testing.T) {
		v, ok, err := src.Resolve(context.Background())
		assert.NoError(t, err)
		assert.False(t, ok)
		assert.Empty(t, v)
	})

	t.Run("header absent from map", func(t *testing.T) {
		ctx := WithForwardedHeaders(context.Background(), map[string]string{
			"X-Other": "something",
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.False(t, ok)
		assert.Empty(t, v)
	})

	t.Run("header present but empty", func(t *testing.T) {
		ctx := WithForwardedHeaders(context.Background(), map[string]string{
			"X-Grafana-Id": "",
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.False(t, ok)
		assert.Empty(t, v)
	})
}

// ---------------------------------------------------------------------------
// jwtValueSource.Resolve — Bearer prefix stripping
// ---------------------------------------------------------------------------

func TestJWTValueSource_BearerPrefix(t *testing.T) {
	claimVal := "tenant_abc"

	cases := []struct {
		name   string
		prefix string
	}{
		{"lowercase bearer", "bearer "},
		{"uppercase BEARER", "BEARER "},
		{"mixed case Bearer", "Bearer "},
		{"no prefix", ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rawToken := makeTestJWT(t, jwt.MapClaims{
				"tenants": claimVal,
				"exp":     time.Now().Add(time.Hour).Unix(),
			})
			src := &jwtValueSource{
				settingName: "tenant",
				headerName:  "X-Grafana-Id",
				claimPath:   []string{"tenants"},
				joinSep:     ",",
				verify:      CustomSettingJWTVerifyNone,
			}
			ctx := jwtCtx("X-Grafana-Id", tc.prefix+rawToken)
			v, ok, err := src.Resolve(ctx)
			assert.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, claimVal, v)
		})
	}
}

// ---------------------------------------------------------------------------
// jwtValueSource.Resolve — malformed token semantics
// ---------------------------------------------------------------------------

// Under verify=none a malformed token is treated as "value absent" so the
// OnMissing policy on the binding decides the outcome. This lets operators
// configure OnMissing=empty on best-effort paths (e.g. alerting) without
// having their queries hard-fail on a corrupted forwarded token.
func TestJWTValueSource_MalformedToken_VerifyNone_TreatedAsAbsent(t *testing.T) {
	src := &jwtValueSource{
		settingName: "tenant",
		headerName:  "X-Grafana-Id",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyNone,
	}

	ctx := jwtCtx("X-Grafana-Id", "this.is.not.a.valid.jwt")
	val, ok, err := src.Resolve(ctx)
	assert.NoError(t, err, "malformed JWT under verify=none must NOT hard-fail; OnMissing decides")
	assert.False(t, ok, "malformed JWT under verify=none must report ok=false so OnMissing applies")
	assert.Empty(t, val)
}

// ---------------------------------------------------------------------------
// jwtValueSource.Resolve — claim extraction
// ---------------------------------------------------------------------------

func TestJWTValueSource_ClaimExtraction(t *testing.T) {
	makePathSource := func(path []string) *jwtValueSource {
		return &jwtValueSource{
			settingName: "tenant",
			headerName:  "X-Grafana-Id",
			claimPath:   path,
			joinSep:     ",",
			verify:      CustomSettingJWTVerifyNone,
		}
	}
	makeSource := func(claim string) *jwtValueSource {
		parts := make([]string, 0)
		for _, seg := range splitDot(claim) {
			parts = append(parts, seg)
		}
		return makePathSource(parts)
	}

	makeCtx := func(t *testing.T, claims jwt.MapClaims) context.Context {
		t.Helper()
		token := makeTestJWT(t, claims)
		return jwtCtx("X-Grafana-Id", token)
	}

	t.Run("string claim", func(t *testing.T) {
		src := makeSource("tenants")
		ctx := makeCtx(t, jwt.MapClaims{"tenants": "t1,t2"})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "t1,t2", v)
	})

	t.Run("float64 claim", func(t *testing.T) {
		src := makeSource("count")
		ctx := makeCtx(t, jwt.MapClaims{"count": float64(42)})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "42", v)
	})

	t.Run("float64 claim uses non-scientific formatting where possible", func(t *testing.T) {
		cases := []float64{1000000, 0.5, 1e21}
		for _, claimValue := range cases {
			claimValue := claimValue
			t.Run(strconv.FormatFloat(claimValue, 'f', -1, 64), func(t *testing.T) {
				src := makeSource("count")
				ctx := makeCtx(t, jwt.MapClaims{"count": claimValue})
				v, ok, err := src.Resolve(ctx)
				assert.NoError(t, err)
				assert.True(t, ok)
				assert.Equal(t, strconv.FormatFloat(claimValue, 'f', -1, 64), v)
			})
		}
	})

	t.Run("bool true claim", func(t *testing.T) {
		src := makeSource("active")
		ctx := makeCtx(t, jwt.MapClaims{"active": true})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "true", v)
	})

	t.Run("bool false claim", func(t *testing.T) {
		src := makeSource("active")
		ctx := makeCtx(t, jwt.MapClaims{"active": false})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "false", v)
	})

	t.Run("array of strings joined with comma", func(t *testing.T) {
		src := makeSource("tenants")
		ctx := makeCtx(t, jwt.MapClaims{"tenants": []interface{}{"t1", "t2", "t3"}})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "t1,t2,t3", v)
	})

	t.Run("URI-namespaced top-level claim resolves as one segment", func(t *testing.T) {
		src := makePathSource([]string{"https://myapp.example.com/roles"})
		ctx := makeCtx(t, jwt.MapClaims{"https://myapp.example.com/roles": []interface{}{"admin", "user"}})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "admin,user", v)
	})

	t.Run("array of float64 claims joined with comma", func(t *testing.T) {
		src := makeSource("counts")
		ctx := makeCtx(t, jwt.MapClaims{"counts": []interface{}{float64(1000000), float64(2000000)}})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "1000000,2000000", v)
	})

	t.Run("array presence semantics", func(t *testing.T) {
		cases := []struct {
			name      string
			claim     []interface{}
			wantValue string
			wantOK    bool
		}{
			{name: "empty array is absent", claim: []interface{}{}, wantOK: false},
			{name: "single empty string is present", claim: []interface{}{""}, wantValue: "", wantOK: true},
			{name: "single nil is absent", claim: []interface{}{nil}, wantOK: false},
			{name: "strings are present", claim: []interface{}{"a", "b"}, wantValue: "a,b", wantOK: true},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				src := makeSource("tenants")
				ctx := makeCtx(t, jwt.MapClaims{"tenants": tc.claim})
				v, ok, err := src.Resolve(ctx)
				assert.NoError(t, err)
				assert.Equal(t, tc.wantOK, ok)
				assert.Equal(t, tc.wantValue, v)
			})
		}
	})

	t.Run("array of strings joined with custom separator", func(t *testing.T) {
		src := makeSource("tenants")
		src.joinSep = "|"
		ctx := makeCtx(t, jwt.MapClaims{"tenants": []interface{}{"t1", "t2"}})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "t1|t2", v)
	})

	t.Run("nested a.b.c claim", func(t *testing.T) {
		src := makeSource("a.b.c")
		ctx := makeCtx(t, jwt.MapClaims{
			"a": map[string]interface{}{
				"b": map[string]interface{}{
					"c": "deep_value",
				},
			},
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "deep_value", v)
	})

	t.Run("nested realm_access roles claim", func(t *testing.T) {
		src := makePathSource([]string{"realm_access", "roles"})
		ctx := makeCtx(t, jwt.MapClaims{
			"realm_access": map[string]interface{}{
				"roles": []interface{}{"admin", "user"},
			},
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "admin,user", v)
	})

	t.Run("missing top-level claim returns ok=false", func(t *testing.T) {
		src := makeSource("nonexistent")
		ctx := makeCtx(t, jwt.MapClaims{"other": "val"})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.False(t, ok)
		assert.Empty(t, v)
	})

	t.Run("missing intermediate segment returns ok=false", func(t *testing.T) {
		src := makeSource("a.b.c")
		ctx := makeCtx(t, jwt.MapClaims{
			"a": map[string]interface{}{
				// "b" is absent
			},
		})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.False(t, ok)
		assert.Empty(t, v)
	})

	t.Run("nil claim value treated as missing", func(t *testing.T) {
		src := makeSource("tenants")
		ctx := makeCtx(t, jwt.MapClaims{"tenants": nil})
		v, ok, err := src.Resolve(ctx)
		assert.NoError(t, err)
		assert.False(t, ok)
		assert.Empty(t, v)
	})

	t.Run("object claim returns error", func(t *testing.T) {
		src := makeSource("nested")
		ctx := makeCtx(t, jwt.MapClaims{
			"nested": map[string]interface{}{"key": "val"},
		})
		_, ok, err := src.Resolve(ctx)
		assert.Error(t, err, "object claims should return an error")
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "object")
	})

	t.Run("array containing nested object returns error", func(t *testing.T) {
		src := makeSource("items")
		ctx := makeCtx(t, jwt.MapClaims{
			"items": []interface{}{
				map[string]interface{}{"id": "obj1"},
			},
		})
		_, ok, err := src.Resolve(ctx)
		assert.Error(t, err)
		assert.False(t, ok)
	})
}

// splitDot is a local helper to split dotted paths for tests.
func splitDot(s string) []string {
	result := make([]string, 0)
	for _, seg := range splitString(s, ".") {
		result = append(result, seg)
	}
	return result
}

func splitString(s, sep string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0)
	start := 0
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			out = append(out, s[start:i])
			i += len(sep) - 1
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// ---------------------------------------------------------------------------
// verify=jwks with nil cache returns error on Resolve
// ---------------------------------------------------------------------------

func TestJWTValueSource_JWKSNilCacheReturnsError(t *testing.T) {
	rawToken := makeTestJWT(t, jwt.MapClaims{
		"tenants": "t1",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	src := &jwtValueSource{
		settingName: "tenant",
		headerName:  "X-Grafana-Id",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyJWKS,
		jwksURL:     "https://example.com/keys",
		cache:       nil, // nil cache → validation / health-check path
	}
	ctx := jwtCtx("X-Grafana-Id", rawToken)
	_, ok, err := src.Resolve(ctx)
	assert.Error(t, err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "JWKS")
}

// ---------------------------------------------------------------------------
// verify=none: exp enforcement for non-X-Grafana-Id headers
// ---------------------------------------------------------------------------

// Under verify=none the plugin still enforces `exp` when the token is
// forwarded via a header other than X-Grafana-Id. Grafana validates upstream
// OAuth tokens only at login and forwards them from cache; a stale claim
// binding to a server-side setting is worse than an absent one, so an
// expired token is treated as "absent" and OnMissing decides.
func TestJWTValueSource_VerifyNone_ExpEnforced_ForNonGrafanaHeader(t *testing.T) {
	// Build a syntactically valid but expired token.
	expired := makeTestJWT(t, jwt.MapClaims{
		"tenants": "t1,t2",
		"exp":     float64(time.Now().Add(-1 * time.Hour).Unix()),
	})

	src := &jwtValueSource{
		settingName: "tenant",
		headerName:  "X-Id-Token", // not X-Grafana-Id → freshness must be enforced
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyNone,
	}

	ctx := jwtCtx("X-Id-Token", expired)
	val, ok, err := src.Resolve(ctx)
	assert.NoError(t, err, "expired JWT under verify=none must NOT hard-fail; OnMissing decides")
	assert.False(t, ok, "expired JWT under verify=none must report ok=false so OnMissing applies")
	assert.Empty(t, val)
}

// The X-Grafana-Id header is trusted: Grafana re-mints it per request, so
// the plugin does NOT enforce `exp` there under verify=none. This matches
// the documented trust model and avoids false negatives when the plugin's
// clock is skewed relative to Grafana's mint time.
func TestJWTValueSource_VerifyNone_ExpNotEnforced_ForXGrafanaId(t *testing.T) {
	// Build a token whose exp has "passed" but with X-Grafana-Id header.
	// It must be treated as present.
	expired := makeTestJWT(t, jwt.MapClaims{
		"tenants": "t1,t2",
		"exp":     float64(time.Now().Add(-1 * time.Hour).Unix()),
	})

	src := &jwtValueSource{
		settingName: "tenant",
		headerName:  "X-Grafana-Id",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyNone,
	}

	ctx := jwtCtx("X-Grafana-Id", expired)
	val, ok, err := src.Resolve(ctx)
	assert.NoError(t, err)
	assert.True(t, ok, "X-Grafana-Id is trusted; exp not enforced under verify=none")
	assert.Equal(t, "t1,t2", val)
}

// A token whose exp is within the leeway window (60 s in the past) is
// still considered fresh.
func TestJWTValueSource_VerifyNone_ExpLeeway(t *testing.T) {
	tok := makeTestJWT(t, jwt.MapClaims{
		"tenants": "t1",
		"exp":     float64(time.Now().Add(-30 * time.Second).Unix()), // within 60s leeway
	})

	src := &jwtValueSource{
		settingName: "tenant",
		headerName:  "X-Id-Token",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyNone,
	}

	ctx := jwtCtx("X-Id-Token", tok)
	val, ok, err := src.Resolve(ctx)
	assert.NoError(t, err)
	assert.True(t, ok, "exp within 60s leeway must be accepted")
	assert.Equal(t, "t1", val)
}

// A token with no exp claim under verify=none from a non-trusted header is
// still accepted (RFC 7519 makes exp optional). Operators who require exp
// should use verify=jwks with jwt.WithExpirationRequired at parse time.
func TestJWTValueSource_VerifyNone_MissingExp_Accepted(t *testing.T) {
	tok := makeTestJWT(t, jwt.MapClaims{
		"tenants": "t1",
		// no exp
	})

	src := &jwtValueSource{
		settingName: "tenant",
		headerName:  "X-Id-Token",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyNone,
	}

	ctx := jwtCtx("X-Id-Token", tok)
	val, ok, err := src.Resolve(ctx)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "t1", val)
}

func TestJWTValueSource_VerifyNone_NbfEnforcement(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		headerName string
		nbf        interface{}
		wantOK     bool
		wantValue  string
	}{
		{
			name:       "future nbf beyond leeway on non-trusted header is absent",
			headerName: "X-Id-Token",
			nbf:        float64(now.Add(2 * time.Minute).Unix()),
			wantOK:     false,
		},
		{
			name:       "past nbf on non-trusted header is accepted",
			headerName: "X-Id-Token",
			nbf:        float64(now.Add(-time.Minute).Unix()),
			wantOK:     true,
			wantValue:  "t1",
		},
		{
			name:       "future nbf within leeway on non-trusted header is accepted",
			headerName: "X-Id-Token",
			nbf:        float64(now.Add(30 * time.Second).Unix()),
			wantOK:     true,
			wantValue:  "t1",
		},
		{
			name:       "malformed nbf on non-trusted header is absent",
			headerName: "X-Id-Token",
			nbf:        "not-a-number",
			wantOK:     false,
		},
		{
			name:       "future nbf on X-Grafana-Id is accepted",
			headerName: "X-Grafana-Id",
			nbf:        float64(now.Add(2 * time.Minute).Unix()),
			wantOK:     true,
			wantValue:  "t1",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tok := makeTestJWT(t, jwt.MapClaims{
				"tenants": "t1",
				"nbf":     tc.nbf,
			})
			src := &jwtValueSource{
				settingName: "tenant",
				headerName:  tc.headerName,
				claimPath:   []string{"tenants"},
				joinSep:     ",",
				verify:      CustomSettingJWTVerifyNone,
			}

			val, ok, err := src.Resolve(jwtCtx(tc.headerName, tok))
			assert.NoError(t, err)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantValue, val)
		})
	}
}

type countingRoundTripper struct {
	base  http.RoundTripper
	count *int32
}

func (rt countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(rt.count, 1)
	return rt.base.RoundTrip(req)
}

func TestJWKSCache_GetOrCreate_SingleflightColdStart(t *testing.T) {
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, _ := jwksTestServer(t, jwksData)

	client := srv.Client()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var requestCount int32
	client.Transport = countingRoundTripper{base: base, count: &requestCount}

	cache := newJWKSCache(client)
	t.Cleanup(cache.close)

	const goroutines = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			kf, err := cache.getOrCreate(srv.URL)
			if err != nil {
				errs <- err
				return
			}
			if kf == nil {
				errs <- fmt.Errorf("nil keyfunc")
				return
			}
			errs <- nil
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount), "cold-start getOrCreate calls should share one JWKS fetch")
}
