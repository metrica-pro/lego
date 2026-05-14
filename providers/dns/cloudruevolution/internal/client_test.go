package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer mounts an HTTP server that:
//   - responds to POST /auth with a bearer token (counter authCalls)
//   - delegates all other paths to apiHandler (counter apiCalls)
//
// Returns the (*Client, apiCalls, authCalls) tuple so tests can assert.
func newFakeServer(t *testing.T, apiHandler http.HandlerFunc) (*Client, *atomic.Int32, *atomic.Int32) {
	t.Helper()

	var authCalls atomic.Int32
	var apiCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		authCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Distinguish tokens across refreshes so 401-retry assertions can check.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-v" + strconv.Itoa(int(authCalls.Load())),
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		apiHandler(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	apiURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	authURL, err := url.Parse(srv.URL + "/auth")
	require.NoError(t, err)

	c := &Client{
		HTTPClient:            srv.Client(),
		identity:              newIdentity("fake-key", "fake-secret", authURL, srv.Client()),
		apiBaseURL:            apiURL,
		projectID:             "00000000-0000-0000-0000-000000000000",
		OperationPollInterval: DefaultOperationPollInterval,
		OperationTimeout:      DefaultOperationTimeout,
	}
	return c, &apiCalls, &authCalls
}

func TestClient_do_Success(t *testing.T) {
	c, apiCalls, authCalls := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Validate auth + UA headers reached the API.
		assert.Equal(t, "Bearer tok-v1", r.Header.Get("Authorization"))
		assert.NotEmpty(t, r.Header.Get("User-Agent"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	})

	var out struct {
		Hello string `json:"hello"`
	}
	require.NoError(t, c.do(t.Context(), http.MethodGet, "/v1/something", nil, nil, &out))
	assert.Equal(t, "world", out.Hello)
	assert.Equal(t, int32(1), apiCalls.Load())
	assert.Equal(t, int32(1), authCalls.Load())
}

func TestClient_do_RetriesOn401(t *testing.T) {
	var seen atomic.Int32

	c, apiCalls, authCalls := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		switch seen.Load() {
		case 1:
			// First attempt: server says token is invalid.
			assert.Equal(t, "Bearer tok-v1", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
		default:
			// Second attempt: identity refreshed → new token.
			assert.Equal(t, "Bearer tok-v2", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	})

	require.NoError(t, c.do(t.Context(), http.MethodGet, "/v1/probe", nil, nil, nil))

	assert.Equal(t, int32(2), apiCalls.Load(), "expected 2 API requests (initial + retry)")
	assert.Equal(t, int32(2), authCalls.Load(), "expected 2 auth requests (initial + refresh)")
}

func TestClient_do_Persistent401(t *testing.T) {
	c, apiCalls, _ := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	err := c.do(t.Context(), http.MethodGet, "/v1/probe", nil, nil, nil)
	require.Error(t, err)
	assert.Equal(t, int32(2), apiCalls.Load(), "must not retry more than once")
}

func TestClient_do_APIErrorEnvelope(t *testing.T) {
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":6,"message":"already exists","details":[]}`))
	})

	err := c.do(t.Context(), http.MethodPost, "/v1/publicRecordsSole",
		nil, map[string]string{"name": "x"}, nil)
	require.Error(t, err)
	assert.True(t, IsAlreadyExists(err), "expected IsAlreadyExists, got %T %v", err, err)
	assert.False(t, IsNotFound(err))
}

func TestClient_do_NotFoundEnvelope(t *testing.T) {
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":9,"message":"precondition","details":[{"@type":"google.rpc.ResourceInfo","resourceType":"shared record","description":"shared record not found"}]}`))
	})

	err := c.do(t.Context(), http.MethodDelete, "/v1/publicRecordsSole/abc", nil, nil, nil)
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
}

func TestClient_do_UnknownErrorFallback(t *testing.T) {
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<html>oops</html>`))
	})

	err := c.do(t.Context(), http.MethodGet, "/v1/probe", nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")
	assert.Contains(t, err.Error(), "500")
}

func TestClient_do_SendsJSONBody(t *testing.T) {
	var got []byte

	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	body := map[string]any{"name": "rec", "type": "txt"}
	require.NoError(t, c.do(t.Context(), http.MethodPost, "/v1/publicRecordsSole", nil, body, nil))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(got, &decoded))
	assert.Equal(t, "rec", decoded["name"])
	assert.Equal(t, "txt", decoded["type"])
}

func TestClient_do_AppliesQuery(t *testing.T) {
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "pid-1", r.URL.Query().Get("projectId"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	q := url.Values{}
	q.Set("projectId", "pid-1")
	require.NoError(t, c.do(t.Context(), http.MethodGet, "/v1/publicZones", q, nil, nil))
}

func TestParseAPIError(t *testing.T) {
	cases := []struct {
		name string
		body string
		want *APIError
	}{
		{"empty", "", nil},
		{"not envelope", `{"foo":"bar"}`, nil},
		{"valid",
			`{"code":6,"message":"already exists"}`,
			&APIError{Code: 6, Message: "already exists", HTTPStatus: 409}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAPIError([]byte(tc.body), 409)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.want.Code, got.Code)
			assert.Equal(t, tc.want.Message, got.Message)
			assert.Equal(t, tc.want.HTTPStatus, got.HTTPStatus)
		})
	}
}
