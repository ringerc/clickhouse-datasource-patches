package plugin

import (
	"context"
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
			JWTClaim:      "tenants",
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
			Setting:   "custom_tenant",
			Enforced:  true,
			Source:    CustomSettingSourceJWT,
			JWTClaim:  "tenants",
			JWTVerify: "none",
			OnMissing: onMissingEmpty,
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
		assert.Contains(t, err.Error(), "jwtClaim")
	})

	t.Run("JWT source with value set is rejected", func(t *testing.T) {
		_, err := BuildEnforcedBinding(CustomSetting{
			Setting:  "s1",
			Enforced: true,
			Source:   CustomSettingSourceJWT,
			JWTClaim: "tenants",
			Value:    "foo",
		}, EnforcedSourceRuntime{})
		assert.Error(t, err)
	})

	t.Run("JWT source binding readonly is rejected", func(t *testing.T) {
		for _, name := range []string{"readonly", "READONLY", "ReadOnly"} {
			name := name
			t.Run(name, func(t *testing.T) {
				_, err := BuildEnforcedBinding(CustomSetting{
					Setting:  name,
					Enforced: true,
					Source:   CustomSettingSourceJWT,
					JWTClaim: "tenants",
				}, EnforcedSourceRuntime{})
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "reserved")
			})
		}
	})

	t.Run("verify=jwks with nil cache: source built but cache is nil", func(t *testing.T) {
		b, err := BuildEnforcedBinding(CustomSetting{
			Setting:    "s1",
			Enforced:   true,
			Source:     CustomSettingSourceJWT,
			JWTClaim:   "tenants",
			JWTVerify:  "jwks",
			JWTJWKSURL: "https://example.com/keys",
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
			JWTClaim:      "tenants",
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
	makeSource := func(claim string) *jwtValueSource {
		parts := make([]string, 0)
		for _, seg := range splitDot(claim) {
			parts = append(parts, seg)
		}
		return &jwtValueSource{
			settingName: "tenant",
			headerName:  "X-Grafana-Id",
			claimPath:   parts,
			joinSep:     ",",
			verify:      CustomSettingJWTVerifyNone,
		}
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
