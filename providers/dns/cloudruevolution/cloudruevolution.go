// Package cloudruevolution implements a DNS-01 challenge provider for
// Cloud.ru Evolution DNS (https://dns.api.cloud.ru).
//
// It is distinct from the legacy "cloudru" provider, which targets the
// older console.cloud.ru/api/clouddns endpoint.
package cloudruevolution

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/platform/env"
	"github.com/go-acme/lego/v5/providers/dns/cloudruevolution/internal"
	"github.com/go-acme/lego/v5/providers/dns/internal/clientdebug"
)

// Environment variable names.
const (
	envNamespace = "CLOUDRU_EVOLUTION_"

	EnvKeyID     = envNamespace + "KEY_ID"
	EnvSecret    = envNamespace + "SECRET"
	EnvProjectID = envNamespace + "PROJECT_ID"

	EnvTTL                = envNamespace + "TTL"
	EnvPropagationTimeout = envNamespace + "PROPAGATION_TIMEOUT"
	EnvPollingInterval    = envNamespace + "POLLING_INTERVAL"
	EnvHTTPTimeout        = envNamespace + "HTTP_TIMEOUT"
	EnvOperationTimeout   = envNamespace + "OPERATION_TIMEOUT"
	EnvAPIEndpoint        = envNamespace + "API_ENDPOINT"
	EnvAuthEndpoint       = envNamespace + "AUTH_ENDPOINT"
)

// Default endpoints (verified live 2026-05-14).
const (
	defaultAPIEndpoint  = "https://dns.api.cloud.ru"
	defaultAuthEndpoint = "https://iam.api.cloud.ru/api/v1/auth/token"
)

var _ challenge.ProviderTimeout = (*DNSProvider)(nil)

// Config is used to configure the creation of the DNSProvider.
type Config struct {
	KeyID     string
	Secret    string
	ProjectID string

	APIEndpoint  string
	AuthEndpoint string

	TTL                int
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
	OperationTimeout   time.Duration
	HTTPClient         *http.Client
}

// NewDefaultConfig returns a default configuration for the DNSProvider,
// reading optional values from the environment.
func NewDefaultConfig() *Config {
	return &Config{
		APIEndpoint:        env.GetOrDefaultString(EnvAPIEndpoint, defaultAPIEndpoint),
		AuthEndpoint:       env.GetOrDefaultString(EnvAuthEndpoint, defaultAuthEndpoint),
		TTL:                env.GetOrDefaultInt(EnvTTL, dns01.DefaultTTL),
		PropagationTimeout: env.GetOrDefaultSecond(EnvPropagationTimeout, 5*time.Minute),
		PollingInterval:    env.GetOrDefaultSecond(EnvPollingInterval, 5*time.Second),
		OperationTimeout:   env.GetOrDefaultSecond(EnvOperationTimeout, 2*time.Minute),
		HTTPClient: &http.Client{
			Timeout: env.GetOrDefaultSecond(EnvHTTPTimeout, 30*time.Second),
		},
	}
}

// recordState is what the provider remembers per ACME token so CleanUp
// can later remove the right value from a possibly-shared rrset.
type recordState struct {
	recordID string
	value    string
}

// DNSProvider implements challenge.ProviderTimeout against the Cloud.ru
// Evolution DNS public-zones API.
type DNSProvider struct {
	config *Config
	client *internal.Client

	records   map[string]recordState
	recordsMu sync.Mutex
}

// NewDNSProvider returns a DNSProvider instance configured for Cloud.ru
// Evolution DNS. Credentials must be passed in the environment variables:
// CLOUDRU_EVOLUTION_KEY_ID, CLOUDRU_EVOLUTION_SECRET, CLOUDRU_EVOLUTION_PROJECT_ID.
func NewDNSProvider() (*DNSProvider, error) {
	values, err := env.Get(EnvKeyID, EnvSecret, EnvProjectID)
	if err != nil {
		return nil, fmt.Errorf("cloudruevolution: %w", err)
	}

	config := NewDefaultConfig()
	config.KeyID = values[EnvKeyID]
	config.Secret = values[EnvSecret]
	config.ProjectID = values[EnvProjectID]

	return NewDNSProviderConfig(config)
}

// NewDNSProviderConfig returns a DNSProvider instance configured with the
// provided Config.
func NewDNSProviderConfig(config *Config) (*DNSProvider, error) {
	if config == nil {
		return nil, errors.New("cloudruevolution: the configuration of the DNS provider is nil")
	}

	if config.KeyID == "" || config.Secret == "" || config.ProjectID == "" {
		return nil, errors.New("cloudruevolution: some credentials information are missing")
	}

	if config.APIEndpoint == "" {
		config.APIEndpoint = defaultAPIEndpoint
	}

	if config.AuthEndpoint == "" {
		config.AuthEndpoint = defaultAuthEndpoint
	}

	client, err := internal.NewClient(config.KeyID, config.Secret, config.ProjectID,
		config.APIEndpoint, config.AuthEndpoint)
	if err != nil {
		return nil, fmt.Errorf("cloudruevolution: %w", err)
	}

	if config.HTTPClient != nil {
		client.HTTPClient = config.HTTPClient
	}

	client.HTTPClient = clientdebug.Wrap(client.HTTPClient)

	if config.PollingInterval > 0 {
		client.OperationPollInterval = config.PollingInterval
	}

	if config.OperationTimeout > 0 {
		client.OperationTimeout = config.OperationTimeout
	}

	return &DNSProvider{
		config:  config,
		client:  client,
		records: make(map[string]recordState),
	}, nil
}

// Present creates (or appends to) the TXT record for the ACME DNS-01 challenge.
//
// Wildcard certificates request both the apex and the *-prefixed name, which
// resolve to the same _acme-challenge label. In that case the provider must
// merge the new value into the existing rrset, not POST a second record.
func (d *DNSProvider) Present(ctx context.Context, domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(ctx, domain, keyAuth)

	authZone, err := dns01.DefaultClient().FindZoneByFqdn(ctx, info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("cloudruevolution: could not find zone for domain %q: %w", domain, err)
	}

	return d.presentForZone(ctx, authZone, info, token)
}

// presentForZone is the testable core of Present — everything after the
// dns01 SOA discovery has produced authZone. The split exists so unit tests
// can drive the API surface without relying on the live DNS resolver.
func (d *DNSProvider) presentForZone(ctx context.Context, authZone string, info dns01.ChallengeInfo, token string) error {
	zone, err := d.client.FindZoneByDomain(ctx, authZone)
	if err != nil {
		return fmt.Errorf("cloudruevolution: lookup zone for %q: %w", authZone, err)
	}

	if zone == nil {
		return fmt.Errorf("cloudruevolution: zone %q not found in project %s",
			dns01.UnFqdn(authZone), d.config.ProjectID)
	}

	relName := relativeName(info.EffectiveFQDN, authZone)

	recordID, err := d.upsertTXT(ctx, zone.ID, relName, info.Value)
	if err != nil {
		return fmt.Errorf("cloudruevolution: present %s: %w", info.EffectiveFQDN, err)
	}

	d.recordsMu.Lock()
	d.records[token] = recordState{recordID: recordID, value: info.Value}
	d.recordsMu.Unlock()

	return nil
}

// upsertTXT either creates a new TXT rrset or merges value into the existing
// one. A 409 from CreateRecord triggers the merge path (covers races between
// two parallel Present calls for the same _acme-challenge label).
func (d *DNSProvider) upsertTXT(ctx context.Context, zoneID, name, value string) (string, error) {
	existing, err := d.client.FindTXTRecord(ctx, zoneID, name)
	if err != nil {
		return "", fmt.Errorf("list records: %w", err)
	}

	if existing == nil {
		recordID, err := d.client.CreateRecordAndWait(ctx, internal.CreateRecordRequest{
			PublicZoneID: zoneID,
			Name:         name,
			Type:         internal.RecordTypeTXT,
			Values:       []string{value},
			TTL:          d.config.TTL,
		})
		if err == nil {
			return recordID, nil
		}

		if !internal.IsAlreadyExists(err) {
			return "", fmt.Errorf("create record: %w", err)
		}

		// Lost the race; refetch and fall through to the merge path.
		existing, err = d.client.FindTXTRecord(ctx, zoneID, name)
		if err != nil {
			return "", fmt.Errorf("refetch after 409: %w", err)
		}

		if existing == nil {
			return "", errors.New("create returned 409 but record disappeared")
		}
	}

	if slices.Contains(existing.Values, value) {
		return existing.ID, nil
	}

	merged := append(append([]string{}, existing.Values...), value)
	if err := d.client.UpdateRecordAndWait(ctx, existing.ID, internal.UpdateRecordRequest{
		Values: merged,
		TTL:    d.config.TTL,
	}); err != nil {
		return "", fmt.Errorf("update record: %w", err)
	}

	return existing.ID, nil
}

// CleanUp removes the value Present added; if the rrset becomes empty it is
// deleted, otherwise it is patched to retain the remaining values.
//
// Lost-bookkeeping recovery: if the in-memory map does not know the recordID
// (e.g. the process was restarted between Present and CleanUp) the rrset is
// located by name within the zone.
func (d *DNSProvider) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(ctx, domain, keyAuth)

	authZone, err := dns01.DefaultClient().FindZoneByFqdn(ctx, info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("cloudruevolution: could not find zone for domain %q: %w", domain, err)
	}

	return d.cleanupForZone(ctx, authZone, info, token)
}

// cleanupForZone is the testable counterpart to presentForZone.
func (d *DNSProvider) cleanupForZone(ctx context.Context, authZone string, info dns01.ChallengeInfo, token string) error {
	zone, err := d.client.FindZoneByDomain(ctx, authZone)
	if err != nil {
		return fmt.Errorf("cloudruevolution: lookup zone for %q: %w", authZone, err)
	}

	if zone == nil {
		return nil // Nothing to clean up.
	}

	recordID, value, err := d.resolveCleanupTarget(ctx, zone.ID, relativeName(info.EffectiveFQDN, authZone), info.Value, token)
	if err != nil {
		return err
	}

	if recordID == "" {
		return nil
	}

	return d.removeValueFromRecord(ctx, recordID, value)
}

// resolveCleanupTarget returns the recordID and value to remove for CleanUp.
// It uses the in-memory token→state map when available, falling back to a
// zone-wide list-and-match. An empty recordID is a successful "nothing to do".
func (d *DNSProvider) resolveCleanupTarget(ctx context.Context, zoneID, relName, fallbackValue, token string) (string, string, error) {
	d.recordsMu.Lock()

	state, ok := d.records[token]
	if ok {
		delete(d.records, token)
	}

	d.recordsMu.Unlock()

	if ok && state.recordID != "" {
		return state.recordID, state.value, nil
	}

	existing, err := d.client.FindTXTRecord(ctx, zoneID, relName)
	if err != nil {
		return "", "", fmt.Errorf("cloudruevolution: cleanup lookup: %w", err)
	}

	if existing == nil {
		return "", "", nil
	}

	return existing.ID, fallbackValue, nil
}

// removeValueFromRecord fetches the current rrset, drops the supplied value,
// and either deletes the record (last value removed) or patches it.
// not-found responses are treated as success.
func (d *DNSProvider) removeValueFromRecord(ctx context.Context, recordID, value string) error {
	rec, err := d.client.GetRecord(ctx, recordID)
	if err != nil {
		if internal.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("cloudruevolution: cleanup get %s: %w", recordID, err)
	}

	remaining := filterOut(rec.Values, value)

	if len(remaining) == 0 {
		if delErr := d.client.DeleteRecordAndWait(ctx, recordID); delErr != nil && !internal.IsNotFound(delErr) {
			return fmt.Errorf("cloudruevolution: delete %s: %w", recordID, delErr)
		}

		return nil
	}

	if patchErr := d.client.UpdateRecordAndWait(ctx, recordID, internal.UpdateRecordRequest{
		Values: remaining,
		TTL:    rec.TTL,
	}); patchErr != nil {
		return fmt.Errorf("cloudruevolution: cleanup patch %s: %w", recordID, patchErr)
	}

	return nil
}

// relativeName strips the parent zone suffix from a fully-qualified host name.
// For an apex challenge (e.g. _acme-challenge.example.com on zone example.com)
// the result is "_acme-challenge"; for a wildcard challenge over the apex it
// is "". The Cloud.ru API requires zone-relative names without trailing dots.
func relativeName(fqdn, authZone string) string {
	host := strings.TrimSuffix(fqdn, ".")

	zone := strings.TrimSuffix(authZone, ".")
	if strings.EqualFold(host, zone) {
		return ""
	}

	suffix := "." + zone
	if strings.HasSuffix(strings.ToLower(host), strings.ToLower(suffix)) {
		return host[:len(host)-len(suffix)]
	}

	return host
}

// filterOut returns a new slice with all occurrences of target removed.
func filterOut(values []string, target string) []string {
	out := make([]string, 0, len(values))

	for _, v := range values {
		if v != target {
			out = append(out, v)
		}
	}

	return out
}

// Timeout returns the timeout and interval used when checking for DNS propagation.
func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}
