package cloudruevolution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/internal/tester"
	"github.com/go-acme/lego/v5/providers/dns/cloudruevolution/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const envDomain = envNamespace + "DOMAIN"

var envTest = tester.NewEnvTest(
	EnvKeyID,
	EnvSecret,
	EnvProjectID,
).WithDomain(envDomain)

// ---------- env / config tests ----------

func TestNewDNSProvider(t *testing.T) {
	testCases := []struct {
		desc     string
		envVars  map[string]string
		expected string
	}{
		{
			desc: "success",
			envVars: map[string]string{
				EnvKeyID:     "user",
				EnvSecret:    "secret",
				EnvProjectID: "00000000-0000-0000-0000-000000000000",
			},
		},
		{
			desc:     "missing credentials",
			envVars:  map[string]string{},
			expected: "cloudruevolution: some credentials information are missing: CLOUDRU_EVOLUTION_KEY_ID,CLOUDRU_EVOLUTION_SECRET,CLOUDRU_EVOLUTION_PROJECT_ID",
		},
		{
			desc: "missing key ID",
			envVars: map[string]string{
				EnvSecret:    "secret",
				EnvProjectID: "00000000-0000-0000-0000-000000000000",
			},
			expected: "cloudruevolution: some credentials information are missing: CLOUDRU_EVOLUTION_KEY_ID",
		},
		{
			desc: "missing secret",
			envVars: map[string]string{
				EnvKeyID:     "user",
				EnvProjectID: "00000000-0000-0000-0000-000000000000",
			},
			expected: "cloudruevolution: some credentials information are missing: CLOUDRU_EVOLUTION_SECRET",
		},
		{
			desc: "missing project id",
			envVars: map[string]string{
				EnvKeyID:  "user",
				EnvSecret: "secret",
			},
			expected: "cloudruevolution: some credentials information are missing: CLOUDRU_EVOLUTION_PROJECT_ID",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			defer envTest.RestoreEnv()

			envTest.ClearEnv()
			envTest.Apply(tc.envVars)

			p, err := NewDNSProvider()

			if tc.expected == "" {
				require.NoError(t, err)
				require.NotNil(t, p)
				require.NotNil(t, p.config)
				require.NotNil(t, p.client)

				return
			}

			require.EqualError(t, err, tc.expected)
		})
	}
}

func TestNewDNSProviderConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := NewDNSProviderConfig(nil)
		require.EqualError(t, err, "cloudruevolution: the configuration of the DNS provider is nil")
	})

	t.Run("missing credentials", func(t *testing.T) {
		_, err := NewDNSProviderConfig(NewDefaultConfig())
		require.EqualError(t, err, "cloudruevolution: some credentials information are missing")
	})

	t.Run("ok", func(t *testing.T) {
		cfg := NewDefaultConfig()
		cfg.KeyID = "user"
		cfg.Secret = "secret"
		cfg.ProjectID = "00000000-0000-0000-0000-000000000000"
		p, err := NewDNSProviderConfig(cfg)
		require.NoError(t, err)
		require.NotNil(t, p)
	})

	t.Run("defaults filled when blank", func(t *testing.T) {
		cfg := &Config{
			KeyID:     "u",
			Secret:    "s",
			ProjectID: "p",
		}
		_, err := NewDNSProviderConfig(cfg)
		require.NoError(t, err)
		assert.Equal(t, defaultAPIEndpoint, cfg.APIEndpoint)
		assert.Equal(t, defaultAuthEndpoint, cfg.AuthEndpoint)
	})
}

func TestDNSProvider_Timeout(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.KeyID = "u"
	cfg.Secret = "s"
	cfg.ProjectID = "p"
	cfg.PropagationTimeout = 7 * time.Minute
	cfg.PollingInterval = 11 * time.Second

	p, err := NewDNSProviderConfig(cfg)
	require.NoError(t, err)

	timeout, interval := p.Timeout()
	assert.Equal(t, 7*time.Minute, timeout)
	assert.Equal(t, 11*time.Second, interval)
}

// ---------- mocked end-to-end Present/CleanUp ----------

// mockEvolutionDNS spins up a minimal but realistic Cloud.ru-shaped HTTP
// server: token endpoint, /v1/publicZones, /v1/publicRecordsSole CRUD, and a
// counted /v1/operations/{id} that flips Done after the first poll.
type mockEvolutionDNS struct {
	t   *testing.T
	srv *httptest.Server

	zoneID    string
	zoneName  string
	projectID string

	mu      sync.Mutex
	records map[string]*internal.PublicRecord // id → record
	nextID  int

	// pendingOps stores operations that have not been polled yet.
	pendingOps map[string]*internal.Operation
}

func newMockEvolutionDNS(t *testing.T, zoneName string) *mockEvolutionDNS { //nolint:unparam // single-zone today, reserved for future multi-zone tests
	t.Helper()

	m := &mockEvolutionDNS{
		t:          t,
		zoneID:     "zone-1",
		zoneName:   zoneName,
		projectID:  "00000000-0000-0000-0000-000000000000",
		records:    map[string]*internal.PublicRecord{},
		pendingOps: map[string]*internal.Operation{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth", m.handleAuth)
	mux.HandleFunc("/v1/publicZones", m.handleZones)
	mux.HandleFunc("/v1/publicRecordsSole", m.handleRecords)
	mux.HandleFunc("/v1/publicRecordsSole/", m.handleRecordByID)
	mux.HandleFunc("/v1/operations/", m.handleOperation)

	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)

	return m
}

func (m *mockEvolutionDNS) handleAuth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "mock-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

func (m *mockEvolutionDNS) handleZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Query().Get("projectId") != m.projectID {
		http.Error(w, "wrong project", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(internal.ListZonesResponse{
		Zones: []internal.PublicZone{{
			ID:        m.zoneID,
			ProjectID: m.projectID,
			Name:      m.zoneName,
			Domain:    m.zoneName + ".",
			State:     "ok",
		}},
	})
}

func (m *mockEvolutionDNS) handleRecords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("publicZoneId") != m.zoneID {
			http.Error(w, "wrong zone", http.StatusBadRequest)
			return
		}

		m.mu.Lock()

		recs := make([]internal.PublicRecord, 0, len(m.records))
		for _, rec := range m.records {
			recs = append(recs, *rec)
		}
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(internal.ListRecordsResponse{Records: recs})

	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)

		var req internal.CreateRecordRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		// Replay the 409-on-duplicate semantics of the real API: a record
		// with the same name+type cannot exist twice.
		m.mu.Lock()
		for _, rec := range m.records {
			if rec.Name != req.Name || rec.Type != req.Type {
				continue
			}
			m.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":6,"message":"already exists"}`))

			return
		}

		m.nextID++
		id := "rec-" + intToString(m.nextID)
		m.records[id] = &internal.PublicRecord{
			ID:           id,
			PublicZoneID: req.PublicZoneID,
			Name:         req.Name,
			Type:         req.Type,
			Values:       append([]string{}, req.Values...),
			TTL:          req.TTL,
		}
		m.mu.Unlock()
		m.replyOp(w, id, "create")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *mockEvolutionDNS) handleRecordByID(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/v1/publicRecordsSole/"):]
	switch r.Method {
	case http.MethodGet:
		m.mu.Lock()
		rec, ok := m.records[id]
		m.mu.Unlock()

		if !ok {
			m.replyNotFound(w)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec)

	case http.MethodPatch:
		body, _ := io.ReadAll(r.Body)

		var req internal.UpdateRecordRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}

		m.mu.Lock()

		rec, ok := m.records[id]
		if !ok {
			m.mu.Unlock()
			m.replyNotFound(w)

			return
		}

		rec.Values = append([]string{}, req.Values...)
		if req.TTL > 0 {
			rec.TTL = req.TTL
		}
		m.mu.Unlock()
		m.replyOp(w, id, "update")

	case http.MethodDelete:
		m.mu.Lock()

		_, ok := m.records[id]
		if !ok {
			m.mu.Unlock()
			m.replyNotFound(w)

			return
		}

		delete(m.records, id)
		m.mu.Unlock()
		m.replyOp(w, id, "delete")

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *mockEvolutionDNS) handleOperation(w http.ResponseWriter, r *http.Request) {
	opID := r.URL.Path[len("/v1/operations/"):]

	m.mu.Lock()

	op, ok := m.pendingOps[opID]
	if ok {
		op.Done = true
	}
	m.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(op)
}

func (m *mockEvolutionDNS) replyOp(w http.ResponseWriter, resourceID, _ string) {
	m.mu.Lock()
	m.nextID++
	opID := "op-" + intToString(m.nextID)
	op := &internal.Operation{ID: opID, ResourceID: resourceID, Done: false}
	m.pendingOps[opID] = op
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(op)
}

func (m *mockEvolutionDNS) replyNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"code":9,"message":"precondition","details":[{"@type":"google.rpc.ResourceInfo","resourceType":"shared record","description":"shared record not found"}]}`))
}

func (m *mockEvolutionDNS) listRecords() []internal.PublicRecord {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]internal.PublicRecord, 0, len(m.records))
	for _, r := range m.records {
		out = append(out, *r)
	}

	return out
}

func intToString(i int) string {
	// minimal int-to-string for record IDs; avoids strconv import noise.
	if i == 0 {
		return "0"
	}

	var b [20]byte

	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}

	return string(b[pos:])
}

// newMockedProvider builds a DNSProvider wired against the supplied mock.
func newMockedProvider(t *testing.T, m *mockEvolutionDNS) *DNSProvider {
	t.Helper()

	cfg := NewDefaultConfig()
	cfg.KeyID = "u"
	cfg.Secret = "s"
	cfg.ProjectID = m.projectID
	cfg.APIEndpoint = m.srv.URL
	cfg.AuthEndpoint = m.srv.URL + "/auth"
	cfg.PropagationTimeout = 0 // unused in mocked tests
	cfg.PollingInterval = 5 * time.Millisecond
	cfg.OperationTimeout = 500 * time.Millisecond

	p, err := NewDNSProviderConfig(cfg)
	require.NoError(t, err)

	return p
}

// The full Present/CleanUp flow depends on dns01.FindZoneByFqdn, which makes
// real DNS queries. Rather than swap the global resolver in unit tests, the
// e2e coverage of those paths lives in the //go:build integration suite
// (TestLivePresentCleanUp). Here we exercise the upsert engine that does the
// actual API work, which is what tends to break.

func TestDNSProvider_upsertTXT_Creates(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	recordID, err := p.upsertTXT(context.Background(), m.zoneID, "_acme-challenge", "value-1")
	require.NoError(t, err)
	assert.NotEmpty(t, recordID)

	got := m.listRecords()
	require.Len(t, got, 1)
	assert.Equal(t, "_acme-challenge", got[0].Name)
	assert.Equal(t, "txt", got[0].Type)
	assert.Equal(t, []string{"value-1"}, got[0].Values)
}

func TestDNSProvider_upsertTXT_MergesValueOnExisting(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	_, err := p.upsertTXT(context.Background(), m.zoneID, "_acme-challenge", "value-A")
	require.NoError(t, err)

	_, err = p.upsertTXT(context.Background(), m.zoneID, "_acme-challenge", "value-B")
	require.NoError(t, err)

	got := m.listRecords()
	require.Len(t, got, 1, "must remain a single rrset")
	assert.ElementsMatch(t, []string{"value-A", "value-B"}, got[0].Values)
}

func TestDNSProvider_upsertTXT_NoopOnDuplicateValue(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	_, err := p.upsertTXT(context.Background(), m.zoneID, "_acme-challenge", "same")
	require.NoError(t, err)

	_, err = p.upsertTXT(context.Background(), m.zoneID, "_acme-challenge", "same")
	require.NoError(t, err)

	got := m.listRecords()
	require.Len(t, got, 1)
	assert.Equal(t, []string{"same"}, got[0].Values)
}

// makeChallengeInfo computes the same ChallengeInfo that dns01.GetChallengeInfo
// would, but locally so tests can drive presentForZone / cleanupForZone
// without a live DNS resolver.
func makeChallengeInfo(domain, keyAuth string) dns01.ChallengeInfo {
	// dns01.GetChallengeInfo is pure (hash of keyAuth + format of fqdn) so we
	// can call it with a background ctx.
	return dns01.GetChallengeInfo(context.Background(), domain, keyAuth)
}

func TestDNSProvider_presentForZone_AssignsRecordState(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	info := makeChallengeInfo("example.com", "key-auth-1")
	require.NoError(t, p.presentForZone(context.Background(), "example.com.", info, "tok"))

	recs := m.listRecords()
	require.Len(t, recs, 1)
	assert.Equal(t, "_acme-challenge", recs[0].Name)
	assert.Equal(t, []string{info.Value}, recs[0].Values)

	p.recordsMu.Lock()
	state, ok := p.records["tok"]
	p.recordsMu.Unlock()
	require.True(t, ok)
	assert.Equal(t, recs[0].ID, state.recordID)
	assert.Equal(t, info.Value, state.value)
}

func TestDNSProvider_presentForZone_ZoneMissing(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	info := makeChallengeInfo("foreign.com", "k")
	err := p.presentForZone(context.Background(), "foreign.com.", info, "tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `zone "foreign.com" not found`)
}

func TestDNSProvider_cleanupForZone_DeletesWhenLastValue(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	info := makeChallengeInfo("example.com", "kk")
	require.NoError(t, p.presentForZone(context.Background(), "example.com.", info, "tok-X"))
	require.NoError(t, p.cleanupForZone(context.Background(), "example.com.", info, "tok-X"))

	assert.Empty(t, m.listRecords())
	p.recordsMu.Lock()
	_, ok := p.records["tok-X"]
	p.recordsMu.Unlock()
	assert.False(t, ok)
}

func TestDNSProvider_cleanupForZone_PatchesWhenOtherValuesRemain(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	// lego strips the leading "*." before calling Present, so the wildcard
	// flow drives two challenges on the same FQDN with different keyAuths.
	info1 := makeChallengeInfo("example.com", "v1")
	info2 := makeChallengeInfo("example.com", "v2")

	require.NoError(t, p.presentForZone(context.Background(), "example.com.", info1, "tok-1"))
	require.NoError(t, p.presentForZone(context.Background(), "example.com.", info2, "tok-2"))
	require.Len(t, m.listRecords(), 1, "wildcard + apex must collapse to one rrset")

	require.NoError(t, p.cleanupForZone(context.Background(), "example.com.", info1, "tok-1"))

	recs := m.listRecords()
	require.Len(t, recs, 1)
	assert.Equal(t, []string{info2.Value}, recs[0].Values)
}

func TestDNSProvider_cleanupForZone_FallbackWithoutState(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	info := makeChallengeInfo("example.com", "v")
	require.NoError(t, p.presentForZone(context.Background(), "example.com.", info, "tok-A"))

	// Simulate process restart between Present and CleanUp: drop the
	// in-memory token→record bookkeeping.
	p.recordsMu.Lock()
	p.records = map[string]recordState{}
	p.recordsMu.Unlock()

	require.NoError(t, p.cleanupForZone(context.Background(), "example.com.", info, "tok-A"))
	assert.Empty(t, m.listRecords())
}

func TestDNSProvider_cleanupForZone_NoZoneIsNoop(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	info := makeChallengeInfo("nowhere.com", "v")
	require.NoError(t, p.cleanupForZone(context.Background(), "nowhere.com.", info, "tok"))
}

func TestDNSProvider_cleanupForZone_NoRecordIsNoop(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	info := makeChallengeInfo("example.com", "v")
	require.NoError(t, p.cleanupForZone(context.Background(), "example.com.", info, "tok"))
}

func TestDNSProvider_upsertTXT_MergesIntoPreexisting(t *testing.T) {
	// Mock starts with one TXT record already in the zone; upsertTXT for
	// the same name with a fresh value must produce a single merged rrset.
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	m.mu.Lock()
	m.nextID++
	id := "rec-pre"
	m.records[id] = &internal.PublicRecord{
		ID:           id,
		PublicZoneID: m.zoneID,
		Name:         "_acme-challenge",
		Type:         "txt",
		Values:       []string{"existing"},
		TTL:          120,
	}
	m.mu.Unlock()

	gotID, err := p.upsertTXT(context.Background(), m.zoneID, "_acme-challenge", "fresh")
	require.NoError(t, err)
	assert.Equal(t, id, gotID)

	recs := m.listRecords()
	require.Len(t, recs, 1)
	assert.ElementsMatch(t, []string{"existing", "fresh"}, recs[0].Values)
}

func TestDNSProvider_cleanupForZone_GetRecordNotFoundIsNoop(t *testing.T) {
	m := newMockEvolutionDNS(t, "example.com")
	p := newMockedProvider(t, m)

	info := makeChallengeInfo("example.com", "v")
	// Provide a state pointing at a non-existent record id.
	p.recordsMu.Lock()
	p.records["tok"] = recordState{recordID: "ghost", value: info.Value}
	p.recordsMu.Unlock()

	require.NoError(t, p.cleanupForZone(context.Background(), "example.com.", info, "tok"))
}

func TestAPIError_Error(t *testing.T) {
	err := &internal.APIError{Code: 6, Message: "already exists", HTTPStatus: 409}
	got := err.Error()
	assert.Contains(t, got, "code=6")
	assert.Contains(t, got, "http=409")
	assert.Contains(t, got, "already exists")
}
