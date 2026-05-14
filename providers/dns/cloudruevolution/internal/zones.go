package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// zoneCache memoises the domain→zone mapping. Zones rarely change once a
// project is provisioned, so a process-lifetime cache is safe.
type zoneCache struct {
	mu      sync.RWMutex
	byDomain map[string]*PublicZone
}

func newZoneCache() *zoneCache {
	return &zoneCache{byDomain: make(map[string]*PublicZone)}
}

func (zc *zoneCache) get(domain string) *PublicZone {
	zc.mu.RLock()
	defer zc.mu.RUnlock()
	return zc.byDomain[normalizeDomain(domain)]
}

func (zc *zoneCache) put(z *PublicZone) {
	zc.mu.Lock()
	zc.byDomain[normalizeDomain(z.Domain)] = z
	zc.byDomain[normalizeDomain(z.Name)] = z
	zc.mu.Unlock()
}

func (zc *zoneCache) reset() {
	zc.mu.Lock()
	zc.byDomain = make(map[string]*PublicZone)
	zc.mu.Unlock()
}

// normalizeDomain strips a trailing dot and lowercases for comparison.
func normalizeDomain(s string) string {
	return strings.ToLower(strings.TrimSuffix(s, "."))
}

// ListZones returns all public zones bound to the client's project.
// Pagination is followed transparently.
func (c *Client) ListZones(ctx context.Context) ([]PublicZone, error) {
	q := url.Values{}
	q.Set("projectId", c.projectID)

	var all []PublicZone
	for {
		var page ListZonesResponse
		if err := c.do(ctx, http.MethodGet, "/v1/publicZones", q, nil, &page); err != nil {
			return nil, fmt.Errorf("list zones: %w", err)
		}
		all = append(all, page.Zones...)
		if page.NextPageToken == "" {
			break
		}
		q.Set("pageToken", page.NextPageToken)
	}
	return all, nil
}

// GetZone fetches a single zone by ID.
func (c *Client) GetZone(ctx context.Context, zoneID string) (*PublicZone, error) {
	var z PublicZone
	if err := c.do(ctx, http.MethodGet, "/v1/publicZones/"+zoneID, nil, nil, &z); err != nil {
		return nil, fmt.Errorf("get zone %s: %w", zoneID, err)
	}
	return &z, nil
}

// FindZoneByDomain returns the zone whose Domain (or Name) matches the given
// FQDN. The input may carry a trailing dot or not. Lookups are cached.
// Returns nil if no zone matches.
func (c *Client) FindZoneByDomain(ctx context.Context, domain string) (*PublicZone, error) {
	if c.zones == nil {
		c.zones = newZoneCache()
	}
	if z := c.zones.get(domain); z != nil {
		return z, nil
	}

	zones, err := c.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	wanted := normalizeDomain(domain)
	for i := range zones {
		z := &zones[i]
		if normalizeDomain(z.Domain) == wanted || normalizeDomain(z.Name) == wanted {
			c.zones.put(z)
			return z, nil
		}
	}
	return nil, nil
}

// InvalidateZoneCache drops the memoised zone lookups. Useful after a manual
// zone creation outside this client.
func (c *Client) InvalidateZoneCache() {
	if c.zones != nil {
		c.zones.reset()
	}
}
