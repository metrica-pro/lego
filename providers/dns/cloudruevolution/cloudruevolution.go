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

// Present creates a TXT record using the specified parameters.
// Filled in a later commit (T6).
func (d *DNSProvider) Present(_ context.Context, _, _, _ string) error {
	return errors.New("cloudruevolution: Present not yet implemented")
}

// CleanUp removes the TXT record that Present created.
// Filled in a later commit (T6).
func (d *DNSProvider) CleanUp(_ context.Context, _, _, _ string) error {
	return errors.New("cloudruevolution: CleanUp not yet implemented")
}

// Timeout returns the timeout and interval used when checking for DNS propagation.
func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}
