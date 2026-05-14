package internal

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_ListZones_PassesProjectID(t *testing.T) {
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/publicZones", r.URL.Path)
		assert.Equal(t, "00000000-0000-0000-0000-000000000000", r.URL.Query().Get("projectId"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListZonesResponse{
			Zones: []PublicZone{{ID: "z1", Name: "example.com", Domain: "example.com."}},
		})
	})

	zones, err := c.ListZones(t.Context())
	require.NoError(t, err)
	require.Len(t, zones, 1)
	assert.Equal(t, "z1", zones[0].ID)
}

func TestClient_ListZones_FollowsPagination(t *testing.T) {
	var pages atomic.Int32
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch pages.Add(1) {
		case 1:
			assert.Empty(t, r.URL.Query().Get("pageToken"))
			_ = json.NewEncoder(w).Encode(ListZonesResponse{
				Zones:         []PublicZone{{ID: "a"}, {ID: "b"}},
				NextPageToken: "next",
			})
		case 2:
			assert.Equal(t, "next", r.URL.Query().Get("pageToken"))
			_ = json.NewEncoder(w).Encode(ListZonesResponse{
				Zones: []PublicZone{{ID: "c"}},
			})
		default:
			t.Fatalf("unexpected page request")
		}
	})

	zones, err := c.ListZones(t.Context())
	require.NoError(t, err)
	assert.Len(t, zones, 3)
}

func TestClient_GetZone(t *testing.T) {
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/publicZones/z42", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(PublicZone{ID: "z42", Name: "example.com", State: "ok"})
	})

	z, err := c.GetZone(t.Context(), "z42")
	require.NoError(t, err)
	assert.Equal(t, "ok", z.State)
}

func TestClient_FindZoneByDomain_MatchesWithOrWithoutDot(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1/publicZones", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListZonesResponse{
			Zones: []PublicZone{
				{ID: "z1", Name: "summasoft.ru", Domain: "summasoft.ru."},
				{ID: "z2", Name: "metricapro.ru", Domain: "metricapro.ru."},
			},
		})
	})

	cases := []string{"summasoft.ru", "summasoft.ru.", "SUMMASOFT.RU"}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			z, err := c.FindZoneByDomain(t.Context(), q)
			require.NoError(t, err)
			require.NotNil(t, z)
			assert.Equal(t, "z1", z.ID)
		})
	}
}

func TestClient_FindZoneByDomain_CachesResult(t *testing.T) {
	var calls atomic.Int32
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListZonesResponse{
			Zones: []PublicZone{{ID: "z1", Domain: "ex.com.", Name: "ex.com"}},
		})
	})

	for range 3 {
		z, err := c.FindZoneByDomain(t.Context(), "ex.com")
		require.NoError(t, err)
		require.NotNil(t, z)
	}
	assert.Equal(t, int32(1), calls.Load(), "FindZoneByDomain must reuse cache")

	c.InvalidateZoneCache()
	_, err := c.FindZoneByDomain(t.Context(), "ex.com")
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "InvalidateZoneCache must force refetch")
}

func TestClient_FindZoneByDomain_NoMatch(t *testing.T) {
	c, _, _ := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ListZonesResponse{
			Zones: []PublicZone{{ID: "z1", Domain: "other.com.", Name: "other.com"}},
		})
	})

	z, err := c.FindZoneByDomain(t.Context(), "missing.com")
	require.NoError(t, err)
	assert.Nil(t, z)
}

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"":                "",
		".":               "",
		"Example.com.":    "example.com",
		"summasoft.ru":    "summasoft.ru",
		" foo.com ":       " foo.com ", // we don't strip spaces — guard against regression
	}
	for in, want := range cases {
		t.Run(strings.ReplaceAll(in, " ", "_"), func(t *testing.T) {
			assert.Equal(t, want, normalizeDomain(in))
		})
	}
}
