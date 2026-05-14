//go:build smoke

package cloudruevolution

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/metrica-pro/lego/v5/challenge/dns01"
	"github.com/stretchr/testify/require"
)

// TestSmokePresentCleanUp drives the provider's API surface against the real
// Cloud.ru Evolution DNS endpoint, bypassing the dns01 SOA discovery step
// (which would fail if NS propagation has not completed). The zone is
// supplied explicitly via CLOUDRU_EVOLUTION_DOMAIN.
//
// Build tag `smoke` keeps it out of the default test run; it must be invoked
// explicitly with `go test -tags smoke ./providers/dns/cloudruevolution/`.
func TestSmokePresentCleanUp(t *testing.T) {
	if !envTest.IsLiveTest() {
		t.Skip("smoke test requires CLOUDRU_EVOLUTION_KEY_ID/SECRET/PROJECT_ID/DOMAIN")
	}
	envTest.RestoreEnv()

	provider, err := NewDNSProvider()
	require.NoError(t, err)

	domain := envTest.GetDomain()
	authZone := domain
	if authZone[len(authZone)-1] != '.' {
		authZone += "."
	}

	keyAuth := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	info := dns01.GetChallengeInfo(t.Context(), domain, keyAuth)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	t.Logf("zone=%s fqdn=%s", authZone, info.EffectiveFQDN)

	require.NoError(t, provider.presentForZone(ctx, authZone, info, "smoke-tok"))
	t.Cleanup(func() {
		_ = provider.cleanupForZone(context.Background(), authZone, info, "smoke-tok")
	})

	require.NoError(t, provider.cleanupForZone(ctx, authZone, info, "smoke-tok"))
	// idempotent
	require.NoError(t, provider.cleanupForZone(ctx, authZone, info, "smoke-tok"))
}
