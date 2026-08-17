package plugin

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test RSA key pair (generated once per test run)
// ---------------------------------------------------------------------------

var (
	testRSAKey    *rsa.PrivateKey
	testRSAKeyID  = "test-kid-1"
	testRSAKeyID2 = "test-kid-2"
)

func init() {
	var err error
	testRSAKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key: " + err.Error())
	}
}

// ---------------------------------------------------------------------------
// JWKS server helpers
// ---------------------------------------------------------------------------

// buildJWKSJSON returns a minimal JSON JWK Set containing the public key.
func buildJWKSJSON(kid string, pub *rsa.PublicKey) []byte {
	n := encodeBase64URLUint(pub.N.Bytes())
	e := encodeBase64URLUint(bigIntBytes(pub.E))
	jwks := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":%q,"use":"sig","alg":"RS256","n":%q,"e":%q}]}`, kid, n, e)
	return []byte(jwks)
}

func encodeBase64URLUint(b []byte) string {
	// strip leading zero bytes
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	return base64URLEncode(b)
}

func base64URLEncode(b []byte) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	out := make([]byte, 0, (len(b)*4+2)/3)
	for i := 0; i < len(b); i += 3 {
		var b0, b1, b2 byte
		b0 = b[i]
		if i+1 < len(b) {
			b1 = b[i+1]
		}
		if i+2 < len(b) {
			b2 = b[i+2]
		}
		out = append(out, chars[b0>>2])
		out = append(out, chars[((b0&3)<<4)|(b1>>4)])
		if i+1 < len(b) {
			out = append(out, chars[((b1&0xF)<<2)|(b2>>6)])
		}
		if i+2 < len(b) {
			out = append(out, chars[b2&0x3F])
		}
	}
	return string(out)
}

func bigIntBytes(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte(n & 0xff)}, b...)
		n >>= 8
	}
	return b
}

// jwksTestServer starts an httptest.Server that serves the given JWKS JSON.
// It counts the number of GET requests made to it (for sharing-test assertions).
func jwksTestServer(t *testing.T, jwksData []byte) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var count atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksData)
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

// makeRSAToken builds a signed RS256 JWT.
func makeRSAToken(t *testing.T, claims jwt.MapClaims, key *rsa.PrivateKey, kid string) string {
	t.Helper()
	tkn := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tkn.Header["kid"] = kid
	signed, err := tkn.SignedString(key)
	require.NoError(t, err)
	return signed
}

// makeJWKSSource builds a jwtValueSource backed by a real JWKS server.
func makeJWKSSource(t *testing.T, srv *httptest.Server, settingName, claim, issuer, audience string) *jwtValueSource {
	t.Helper()
	cache := newJWKSCache(srv.Client()) // use TLS-aware test client
	src := &jwtValueSource{
		settingName: settingName,
		headerName:  "X-Token",
		claimPath:   []string{claim},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyJWKS,
		jwksURL:     srv.URL,
		issuer:      issuer,
		audience:    audience,
		cache:       cache,
	}
	return src
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestJWTVerifyJWKS_ValidSignature(t *testing.T) {
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, _ := jwksTestServer(t, jwksData)

	src := makeJWKSSource(t, srv, "tenant", "tenants", "", "")

	token := makeRSAToken(t, jwt.MapClaims{
		"tenants": "t1,t2",
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}, testRSAKey, testRSAKeyID)

	ctx := jwtCtx("X-Token", token)
	v, ok, err := src.Resolve(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "t1,t2", v)
}

func TestJWTVerifyJWKS_ExpiredToken(t *testing.T) {
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, _ := jwksTestServer(t, jwksData)

	src := makeJWKSSource(t, srv, "tenant", "tenants", "", "")

	token := makeRSAToken(t, jwt.MapClaims{
		"tenants": "t1",
		"exp":     time.Now().Add(-time.Hour).Unix(), // expired
		"iat":     time.Now().Add(-2 * time.Hour).Unix(),
	}, testRSAKey, testRSAKeyID)

	ctx := jwtCtx("X-Token", token)
	_, ok, err := src.Resolve(ctx)
	assert.Error(t, err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "expired")
}

func TestJWTVerifyJWKS_WrongSignature(t *testing.T) {
	// Sign with one key, serve JWKS with a different key.
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	// Serve JWKS with testRSAKey (not otherKey)
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, _ := jwksTestServer(t, jwksData)

	src := makeJWKSSource(t, srv, "tenant", "tenants", "", "")

	// Token signed with otherKey but server only knows testRSAKey.
	token := makeRSAToken(t, jwt.MapClaims{
		"tenants": "t1",
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}, otherKey, testRSAKeyID) // same kid, wrong key

	ctx := jwtCtx("X-Token", token)
	_, ok, err := src.Resolve(ctx)
	assert.Error(t, err)
	assert.False(t, ok)
}

func TestJWTVerifyJWKS_IssMismatch(t *testing.T) {
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, _ := jwksTestServer(t, jwksData)

	src := makeJWKSSource(t, srv, "tenant", "tenants", "https://expected.issuer", "")

	token := makeRSAToken(t, jwt.MapClaims{
		"tenants": "t1",
		"iss":     "https://different.issuer",
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}, testRSAKey, testRSAKeyID)

	ctx := jwtCtx("X-Token", token)
	_, ok, err := src.Resolve(ctx)
	assert.Error(t, err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "iss")
}

func TestJWTVerifyJWKS_AudMismatch(t *testing.T) {
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, _ := jwksTestServer(t, jwksData)

	src := makeJWKSSource(t, srv, "tenant", "tenants", "", "expected-audience")

	token := makeRSAToken(t, jwt.MapClaims{
		"tenants": "t1",
		"aud":     []string{"other-audience"},
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}, testRSAKey, testRSAKeyID)

	ctx := jwtCtx("X-Token", token)
	_, ok, err := src.Resolve(ctx)
	assert.Error(t, err)
	assert.False(t, ok)
	assert.Contains(t, err.Error(), "aud")
}

func TestJWTVerifyJWKS_TwoBindingsSameURL_SharedKeyfunc(t *testing.T) {
	// Two sources pointing at the same JWKS URL must share a single keyfunc,
	// meaning the JWKS server should be fetched only once (at construction time).
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, fetchCount := jwksTestServer(t, jwksData)

	// Use the same jwksCache for both sources (as NewDatasource would do).
	cache := newJWKSCache(srv.Client())

	src1 := &jwtValueSource{
		settingName: "tenant1",
		headerName:  "X-Token",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyJWKS,
		jwksURL:     srv.URL,
		cache:       cache,
	}
	src2 := &jwtValueSource{
		settingName: "tenant2",
		headerName:  "X-Token",
		claimPath:   []string{"tenants"},
		joinSep:     ",",
		verify:      CustomSettingJWTVerifyJWKS,
		jwksURL:     srv.URL,
		cache:       cache,
	}

	token := makeRSAToken(t, jwt.MapClaims{
		"tenants": "t1",
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}, testRSAKey, testRSAKeyID)

	ctx := jwtCtx("X-Token", token)

	v1, ok1, err1 := src1.Resolve(ctx)
	v2, ok2, err2 := src2.Resolve(ctx)

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, "t1", v1)
	assert.Equal(t, "t1", v2)

	// Both calls triggered exactly one fetch (initial), not two.
	assert.Equal(t, int64(1), fetchCount.Load(),
		"two bindings with the same JWKS URL should share a single keyfunc (1 fetch)")
}

func TestJWTVerifyJWKS_IssuerAndAudienceMatch(t *testing.T) {
	jwksData := buildJWKSJSON(testRSAKeyID, &testRSAKey.PublicKey)
	srv, _ := jwksTestServer(t, jwksData)

	src := makeJWKSSource(t, srv, "tenant", "tenants", "https://issuer.example", "my-audience")

	token := makeRSAToken(t, jwt.MapClaims{
		"tenants": "t1",
		"iss":     "https://issuer.example",
		"aud":     []string{"my-audience"},
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}, testRSAKey, testRSAKeyID)

	ctx := jwtCtx("X-Token", token)
	v, ok, err := src.Resolve(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "t1", v)
}

// TestJWKSCache_FailureCached verifies that a failing JWKS URL is negatively
// cached to avoid hammering.
func TestJWKSCache_FailureCached(t *testing.T) {
	// Use a server that always returns 500.
	var hitCount atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cache := newJWKSCache(srv.Client())

	_, err1 := cache.getOrCreate(srv.URL)
	assert.Error(t, err1)

	// Second call within TTL should return error WITHOUT hitting the server.
	_, err2 := cache.getOrCreate(srv.URL)
	assert.Error(t, err2)
	assert.Contains(t, err2.Error(), "temporarily unavailable")

	// Server was hit only once (for the first attempt).
	assert.Equal(t, int64(1), hitCount.Load())
}

// Ensure we use encoding/json in this test file.
var _ = json.Marshal
