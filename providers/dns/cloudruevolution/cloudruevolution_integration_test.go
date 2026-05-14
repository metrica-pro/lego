//go:build integration

package cloudruevolution

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/metrica-pro/lego/v5/challenge/dns01"
	"github.com/stretchr/testify/require"
)

// TestLivePresentCleanUp drives the full Present + CleanUp flow against
// the real Cloud.ru Evolution DNS API. Requires the standard
// CLOUDRU_EVOLUTION_* environment variables plus CLOUDRU_EVOLUTION_DOMAIN
// pointing at a zone the credentials may write to (e.g. summasoft.ru).
//
// The test additionally checks that the freshly created TXT becomes
// resolvable through public DNS so that ACME validators (running outside
// our infrastructure) can actually see it. NS propagation through Beget
// can lag — if the apex still resolves to non-Cloud.ru NS the test skips
// with a clear message instead of failing.
func TestLivePresentCleanUp(t *testing.T) {
	if !envTest.IsLiveTest() {
		t.Skip("skipping live test: CLOUDRU_EVOLUTION_KEY_ID/SECRET/PROJECT_ID/DOMAIN required")
	}
	envTest.RestoreEnv()

	domain := envTest.GetDomain()

	if !nsDelegatedToCloudRu(t, domain) {
		t.Skipf("NS for %s not yet delegated to evo-cns0[12].cloud.ru — public resolvers still report the old name servers; skipping", domain)
	}

	provider, err := NewDNSProvider()
	require.NoError(t, err)

	// Distinct value per run prevents parallel CI runs (or rerun of an
	// aborted previous run) from colliding on the same TXT entry.
	keyAuth := fmt.Sprintf("lego-cloudruevolution-integration-%d", time.Now().UnixNano())

	t.Run("Present", func(t *testing.T) {
		require.NoError(t, provider.Present(t.Context(), domain, "", keyAuth))
	})

	t.Run("PublicResolution", func(t *testing.T) {
		info := dns01.GetChallengeInfo(t.Context(), domain, keyAuth)

		// dig +short TXT info.EffectiveFQDN @8.8.8.8
		// Allow up to 90s for public resolvers to see the rrset.
		deadline := time.Now().Add(90 * time.Second)
		for {
			values, err := lookupTXT(t.Context(), info.EffectiveFQDN, "8.8.8.8:53")
			if err == nil {
				for _, v := range values {
					if v == info.Value {
						return
					}
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("TXT %s with value %q not observed on 8.8.8.8 within 90s (last error: %v)",
					info.EffectiveFQDN, info.Value, err)
			}
			time.Sleep(5 * time.Second)
		}
	})

	t.Run("CleanUp", func(t *testing.T) {
		require.NoError(t, provider.CleanUp(t.Context(), domain, "", keyAuth))
	})

	t.Run("CleanUpIsIdempotent", func(t *testing.T) {
		// A second CleanUp must succeed silently — Cloud.ru returns a
		// gRPC-code-9 not-found which the provider swallows.
		require.NoError(t, provider.CleanUp(t.Context(), domain, "", keyAuth))
	})
}

func nsDelegatedToCloudRu(t *testing.T, domain string) bool {
	t.Helper()

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
	nameservers, err := resolver.LookupNS(t.Context(), domain)
	if err != nil {
		t.Logf("LookupNS(%s): %v", domain, err)
		return false
	}
	for _, ns := range nameservers {
		if strings.HasSuffix(strings.ToLower(strings.TrimSuffix(ns.Host, ".")), ".cloud.ru") {
			return true
		}
	}
	return false
}

// lookupTXT performs a direct TXT lookup against a specific resolver,
// returning the joined values. Bypasses the OS resolver cache.
func lookupTXT(ctx context.Context, fqdn, resolverAddr string) ([]string, error) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", resolverAddr)
		},
	}
	return r.LookupTXT(ctx, strings.TrimSuffix(fqdn, "."))
}
