package internal

import (
	"errors"
	"net/http"
	"net/url"
	"time"
)

// Default poll cadence and timeout for asynchronous Cloud.ru operations.
const (
	DefaultOperationPollInterval = 2 * time.Second
	DefaultOperationTimeout      = 2 * time.Minute
)

// Client is an HTTP client for the Cloud.ru Evolution DNS API.
type Client struct {
	HTTPClient *http.Client

	identity *identity

	apiBaseURL *url.URL
	projectID  string

	// OperationPollInterval and OperationTimeout govern WaitForOperation.
	OperationPollInterval time.Duration
	OperationTimeout      time.Duration
}

// NewClient creates a Client. The endpoint defaults are the Cloud.ru production
// hostnames; tests may pass overrides.
func NewClient(keyID, secret, projectID, apiEndpoint, authEndpoint string) (*Client, error) {
	if keyID == "" || secret == "" || projectID == "" {
		return nil, errors.New("missing credentials")
	}

	apiURL, err := url.Parse(apiEndpoint)
	if err != nil {
		return nil, err
	}

	authURL, err := url.Parse(authEndpoint)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	return &Client{
		HTTPClient:            httpClient,
		identity:              newIdentity(keyID, secret, authURL, httpClient),
		apiBaseURL:            apiURL,
		projectID:             projectID,
		OperationPollInterval: DefaultOperationPollInterval,
		OperationTimeout:      DefaultOperationTimeout,
	}, nil
}

// ProjectID returns the project UUID this client is bound to.
func (c *Client) ProjectID() string {
	return c.projectID
}
