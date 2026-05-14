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

func tightTimingsClient(c *Client) {
	c.OperationPollInterval = 5 * time.Millisecond
	c.OperationTimeout = 500 * time.Millisecond
}

// newFakeServerMux is like newFakeServer but lets tests register multiple
// path-specific handlers; the auth handler is mounted on /auth.
func newFakeServerMux(t *testing.T) (*Client, *http.ServeMux) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-static",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	apiURL, _ := url.Parse(srv.URL)
	authURL, _ := url.Parse(srv.URL + "/auth")

	c := &Client{
		HTTPClient:            srv.Client(),
		identity:              newIdentity("k", "s", authURL, srv.Client()),
		apiBaseURL:            apiURL,
		projectID:             "p-1",
		zones:                 newZoneCache(),
		OperationPollInterval: 5 * time.Millisecond,
		OperationTimeout:      500 * time.Millisecond,
	}

	return c, mux
}

func TestClient_CreateRecord_SendsCorrectBody(t *testing.T) {
	c, mux := newFakeServerMux(t)

	mux.HandleFunc("/v1/publicRecordsSole", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		body, _ := io.ReadAll(r.Body)

		var got CreateRecordRequest
		require.NoError(t, json.Unmarshal(body, &got))

		assert.Equal(t, "zone-1", got.PublicZoneID)
		assert.Equal(t, "_acme-challenge", got.Name)
		assert.Equal(t, "txt", got.Type)
		assert.Equal(t, []string{"abc"}, got.Values)
		assert.Equal(t, 120, got.TTL)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-1", ResourceID: "rec-1", Done: false})
	})

	op, err := c.CreateRecord(t.Context(), CreateRecordRequest{
		PublicZoneID: "zone-1", Name: "_acme-challenge", Type: "txt",
		Values: []string{"abc"}, TTL: 120,
	})
	require.NoError(t, err)
	assert.Equal(t, "op-1", op.ID)
	assert.Equal(t, "rec-1", op.ResourceID)
	assert.False(t, op.Done)
}

func TestClient_CreateRecord_DuplicateIsAPIError(t *testing.T) {
	c, mux := newFakeServerMux(t)
	mux.HandleFunc("/v1/publicRecordsSole", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":6,"message":"already exists"}`))
	})

	_, err := c.CreateRecord(t.Context(), CreateRecordRequest{
		PublicZoneID: "z", Name: "n", Type: "txt", Values: []string{"v"}, TTL: 120,
	})
	require.Error(t, err)
	assert.True(t, IsAlreadyExists(err))
}

func TestClient_DeleteRecord_HappyPath(t *testing.T) {
	c, mux := newFakeServerMux(t)
	mux.HandleFunc("/v1/publicRecordsSole/rec-1", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-d", ResourceID: "rec-1", Done: false})
	})

	op, err := c.DeleteRecord(t.Context(), "rec-1")
	require.NoError(t, err)
	assert.Equal(t, "op-d", op.ID)
}

func TestClient_DeleteRecordAndWait_NotFoundIsSuccess(t *testing.T) {
	c, mux := newFakeServerMux(t)
	mux.HandleFunc("/v1/publicRecordsSole/rec-x", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":9,"message":"precondition"}`))
	})

	require.NoError(t, c.DeleteRecordAndWait(t.Context(), "rec-x"))
}

func TestClient_UpdateRecord_PATCH(t *testing.T) {
	c, mux := newFakeServerMux(t)
	mux.HandleFunc("/v1/publicRecordsSole/rec-7", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPatch, r.Method)
		body, _ := io.ReadAll(r.Body)

		var u UpdateRecordRequest
		require.NoError(t, json.Unmarshal(body, &u))
		assert.Equal(t, []string{"a", "b"}, u.Values)
		assert.Equal(t, 60, u.TTL)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-u", ResourceID: "rec-7", Done: false})
	})

	op, err := c.UpdateRecord(t.Context(), "rec-7",
		UpdateRecordRequest{Values: []string{"a", "b"}, TTL: 60})
	require.NoError(t, err)
	assert.Equal(t, "op-u", op.ID)
}

func TestClient_ListRecords_FollowsPagination(t *testing.T) {
	c, mux := newFakeServerMux(t)

	var calls atomic.Int32

	mux.HandleFunc("/v1/publicRecordsSole", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "z-1", r.URL.Query().Get("publicZoneId"))
		w.Header().Set("Content-Type", "application/json")

		switch calls.Add(1) {
		case 1:
			_ = json.NewEncoder(w).Encode(ListRecordsResponse{
				Records:       []PublicRecord{{ID: "r1"}},
				NextPageToken: "page2",
			})
		default:
			assert.Equal(t, "page2", r.URL.Query().Get("pageToken"))

			_ = json.NewEncoder(w).Encode(ListRecordsResponse{
				Records: []PublicRecord{{ID: "r2"}, {ID: "r3"}},
			})
		}
	})

	recs, err := c.ListRecords(t.Context(), "z-1")
	require.NoError(t, err)
	assert.Len(t, recs, 3)
}

func TestClient_FindTXTRecord(t *testing.T) {
	c, mux := newFakeServerMux(t)
	mux.HandleFunc("/v1/publicRecordsSole", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListRecordsResponse{
			Records: []PublicRecord{
				{ID: "ns", Name: "", Type: "ns", Values: []string{"evo-cns01.cloud.ru."}},
				{ID: "tx", Name: "_acme-challenge", Type: "txt", Values: []string{"v1"}},
				{ID: "tx2", Name: "other", Type: "txt", Values: []string{"v2"}},
			},
		})
	})

	got, err := c.FindTXTRecord(t.Context(), "z-1", "_acme-challenge")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "tx", got.ID)

	miss, err := c.FindTXTRecord(t.Context(), "z-1", "nope")
	require.NoError(t, err)
	assert.Nil(t, miss)
}

func TestClient_WaitForOperation_PollsUntilDone(t *testing.T) {
	c, mux := newFakeServerMux(t)
	tightTimingsClient(c)

	var polls atomic.Int32

	mux.HandleFunc("/v1/operations/op-1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		n := polls.Add(1)
		done := n >= 3
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-1", ResourceID: "rec-1", Done: done})
	})

	op, err := c.WaitForOperation(t.Context(), "op-1")
	require.NoError(t, err)
	require.NotNil(t, op)
	assert.Equal(t, "rec-1", op.ResourceID)
	assert.True(t, op.Done)
	assert.GreaterOrEqual(t, polls.Load(), int32(3))
}

func TestClient_WaitForOperation_ReturnsRemoteError(t *testing.T) {
	c, mux := newFakeServerMux(t)
	tightTimingsClient(c)

	mux.HandleFunc("/v1/operations/op-err", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{
			ID: "op-err", Done: true,
			Error: &OperationErr{Code: 9, Message: "precondition"},
		})
	})

	op, err := c.WaitForOperation(t.Context(), "op-err")
	require.Error(t, err)
	require.NotNil(t, op)
	assert.Contains(t, err.Error(), "code=9")
}

func TestClient_WaitForOperation_Timeout(t *testing.T) {
	c, mux := newFakeServerMux(t)
	c.OperationPollInterval = 5 * time.Millisecond
	c.OperationTimeout = 50 * time.Millisecond

	mux.HandleFunc("/v1/operations/op-slow", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-slow", Done: false})
	})

	_, err := c.WaitForOperation(t.Context(), "op-slow")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "time limit exceeded")
}

func TestClient_GetRecord(t *testing.T) {
	c, mux := newFakeServerMux(t)
	mux.HandleFunc("/v1/publicRecordsSole/rec-42", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PublicRecord{
			ID: "rec-42", Name: "_acme-challenge", Type: "txt",
			Values: []string{"a", "b"}, TTL: 60,
		})
	})

	got, err := c.GetRecord(t.Context(), "rec-42")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "rec-42", got.ID)
	assert.Equal(t, []string{"a", "b"}, got.Values)
}

func TestClient_GetRecord_NotFound(t *testing.T) {
	c, mux := newFakeServerMux(t)
	mux.HandleFunc("/v1/publicRecordsSole/missing", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":9,"message":"precondition"}`))
	})

	_, err := c.GetRecord(t.Context(), "missing")
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
}

func TestClient_UpdateRecordAndWait_WaitsForDone(t *testing.T) {
	c, mux := newFakeServerMux(t)
	tightTimingsClient(c)

	mux.HandleFunc("/v1/publicRecordsSole/rec-up", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-up", ResourceID: "rec-up", Done: false})
	})
	mux.HandleFunc("/v1/operations/op-up", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-up", ResourceID: "rec-up", Done: true})
	})

	require.NoError(t, c.UpdateRecordAndWait(t.Context(), "rec-up",
		UpdateRecordRequest{Values: []string{"v1", "v2"}, TTL: 60}))
}

func TestClient_UpdateRecordAndWait_PropagatesError(t *testing.T) {
	c, mux := newFakeServerMux(t)
	tightTimingsClient(c)

	mux.HandleFunc("/v1/publicRecordsSole/rec-up", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":3,"message":"invalid argument"}`))
	})

	err := c.UpdateRecordAndWait(t.Context(), "rec-up",
		UpdateRecordRequest{Values: []string{"v"}, TTL: 60})
	require.Error(t, err)
}

func TestClient_CreateRecordAndWait_ReturnsResourceID(t *testing.T) {
	c, mux := newFakeServerMux(t)
	tightTimingsClient(c)

	mux.HandleFunc("/v1/publicRecordsSole", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-c", ResourceID: "rec-99", Done: false})
	})
	mux.HandleFunc("/v1/operations/op-c", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Operation{ID: "op-c", ResourceID: "rec-99", Done: true})
	})

	id, err := c.CreateRecordAndWait(t.Context(), CreateRecordRequest{
		PublicZoneID: "z", Name: "_acme-challenge", Type: "txt", Values: []string{"v"}, TTL: 60,
	})
	require.NoError(t, err)
	assert.Equal(t, "rec-99", id)
}
