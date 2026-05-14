package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestIdentity returns an identity targeting the given test server URL,
// plus a pointer to the atomic call counter for assertions.
func newTestIdentity(t *testing.T, srv *httptest.Server) *identity {
	t.Helper()

	authURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	return newIdentity("fake-key-id", "fake-secret", authURL, srv.Client())
}

// fakeIAMServer returns an httptest server that responds with the supplied
// access token and TTL. counter is incremented on each request.
func fakeIAMServer(t *testing.T, accessToken string, expiresIn int, counter *atomic.Int32) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)

		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("User-Agent"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var req AuthRequest
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, "fake-key-id", req.KeyID)
		assert.Equal(t, "fake-secret", req.Secret)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
		})
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestIdentity_obtainToken_Success(t *testing.T) {
	var calls atomic.Int32

	srv := fakeIAMServer(t, "tok-A", 3600, &calls)
	id := newTestIdentity(t, srv)

	tok, err := id.obtainToken(t.Context())
	require.NoError(t, err)
	require.NotNil(t, tok)

	assert.Equal(t, "tok-A", tok.AccessToken)
	assert.Equal(t, "Bearer", tok.TokenType)
	assert.Equal(t, 3600, tok.ExpiresIn)
	assert.WithinDuration(t, time.Now().Add(time.Hour), tok.ExpiresAt, 5*time.Second)
	assert.Equal(t, int32(1), calls.Load())
}

func TestIdentity_obtainToken_4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	id := newTestIdentity(t, srv)

	_, err := id.obtainToken(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestIdentity_obtainToken_EmptyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"","expires_in":3600,"token_type":"Bearer"}`))
	}))
	t.Cleanup(srv.Close)

	id := newTestIdentity(t, srv)

	_, err := id.obtainToken(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty access_token")
}

func TestIdentity_obtainToken_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
	}))
	t.Cleanup(srv.Close)

	id := newTestIdentity(t, srv)

	_, err := id.obtainToken(t.Context())
	require.Error(t, err)
}

func TestIdentity_getToken_CachesResult(t *testing.T) {
	var calls atomic.Int32

	srv := fakeIAMServer(t, "cached", 3600, &calls)
	id := newTestIdentity(t, srv)

	first, err := id.getToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "cached", first.AccessToken)

	second, err := id.getToken(t.Context())
	require.NoError(t, err)
	assert.Same(t, first, second)
	assert.Equal(t, int32(1), calls.Load(), "second getToken must hit cache")
}

func TestIdentity_getToken_RefreshAfterInvalidate(t *testing.T) {
	var calls atomic.Int32

	srv := fakeIAMServer(t, "tok", 3600, &calls)
	id := newTestIdentity(t, srv)

	_, err := id.getToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())

	id.invalidate()

	_, err = id.getToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "invalidate must force refresh")
}

func TestIdentity_getToken_ScaledThresholdReusesShortTokens(t *testing.T) {
	// With expires_in=30s the refresh threshold scales to 15s (half), so
	// the cached token is reused rather than every getToken hitting the
	// IAM endpoint — this is the IAM-DoS protection.
	var calls atomic.Int32

	srv := fakeIAMServer(t, "short", 30, &calls)
	id := newTestIdentity(t, srv)

	first, err := id.getToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "short", first.AccessToken)
	assert.Equal(t, int32(1), calls.Load())

	second, err := id.getToken(t.Context())
	require.NoError(t, err)
	assert.Same(t, first, second, "short-lived token must still be cached")
	assert.Equal(t, int32(1), calls.Load(), "no extra IAM call")
}

func TestIdentity_getToken_RefreshesNearExpiry(t *testing.T) {
	// expires_in=1s → threshold=500ms. After ~600ms the token must be
	// refreshed.
	var calls atomic.Int32

	srv := fakeIAMServer(t, "tiny", 1, &calls)
	id := newTestIdentity(t, srv)

	_, err := id.getToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load())

	time.Sleep(600 * time.Millisecond)

	_, err = id.getToken(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "must refresh once threshold crossed")
}

func TestToken_Valid(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name      string
		token     *Token
		threshold time.Duration
		want      bool
	}{
		{"nil", nil, time.Minute, false},
		{"empty access token", &Token{ExpiresAt: now.Add(time.Hour)}, time.Minute, false},
		{"expired", &Token{AccessToken: "x", ExpiresAt: now.Add(-time.Second)}, time.Minute, false},
		{"within threshold", &Token{AccessToken: "x", ExpiresAt: now.Add(30 * time.Second)}, time.Minute, false},
		{"valid", &Token{AccessToken: "x", ExpiresAt: now.Add(2 * time.Hour)}, time.Minute, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.token.Valid(tc.threshold))
		})
	}
}
