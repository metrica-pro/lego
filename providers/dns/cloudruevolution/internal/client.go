package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/go-acme/lego/v5/internal/errutils"
	"github.com/go-acme/lego/v5/internal/useragent"
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

	zones *zoneCache

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
		return nil, fmt.Errorf("parse api endpoint: %w", err)
	}

	authURL, err := url.Parse(authEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse auth endpoint: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	return &Client{
		HTTPClient:            httpClient,
		identity:              newIdentity(keyID, secret, authURL, httpClient),
		apiBaseURL:            apiURL,
		projectID:             projectID,
		zones:                 newZoneCache(),
		OperationPollInterval: DefaultOperationPollInterval,
		OperationTimeout:      DefaultOperationTimeout,
	}, nil
}

// ProjectID returns the project UUID this client is bound to.
func (c *Client) ProjectID() string {
	return c.projectID
}

// do performs an authenticated request and decodes the response.
//
// path is appended to the API base URL. query may be nil. reqBody, if non-nil,
// is JSON-encoded. If respOut is non-nil and the response is 2xx, the body is
// JSON-decoded into it.
//
// On HTTP 401 the cached token is invalidated and the request is retried once
// with a fresh token. Non-2xx responses are returned as *APIError when the
// body decodes to the canonical envelope, otherwise as
// *errutils.UnexpectedStatusCodeError.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, reqBody, respOut any) error {
	var rawBody []byte
	if reqBody != nil {
		var err error
		rawBody, err = json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
	}

	endpoint, err := c.buildURL(path, query)
	if err != nil {
		return err
	}

	for attempt := range 2 {
		req, err := c.newRequest(ctx, method, endpoint, rawBody)
		if err != nil {
			return err
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return errutils.NewHTTPDoError(req, err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return errutils.NewReadResponseError(req, resp.StatusCode, err)
		}

		// Retry once on 401: server rejected our token. Drop the cache so the
		// next newRequest fetches a fresh one.
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.identity.invalidate()
			continue
		}

		if resp.StatusCode/100 != 2 {
			if apiErr := parseAPIError(body, resp.StatusCode); apiErr != nil {
				return apiErr
			}
			// Reconstruct response shape for errutils.
			resp.Body = io.NopCloser(bytes.NewReader(body))
			return errutils.NewUnexpectedResponseStatusCodeError(req, resp)
		}

		if respOut == nil {
			return nil
		}

		if err := json.Unmarshal(body, respOut); err != nil {
			return errutils.NewUnmarshalError(req, resp.StatusCode, body, err)
		}
		return nil
	}

	return errors.New("cloudruevolution: 401 after retry")
}

// newRequest builds an authenticated HTTP request.
func (c *Client) newRequest(ctx context.Context, method, endpoint string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	tok, err := c.identity.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	useragent.SetHeader(req.Header)
	return req, nil
}

// buildURL joins path against the API base URL and attaches query, if any.
func (c *Client) buildURL(path string, query url.Values) (string, error) {
	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("parse path %q: %w", path, err)
	}
	u := c.apiBaseURL.ResolveReference(ref)
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String(), nil
}

// parseAPIError tries to decode body as an APIError. Returns nil if the body
// does not look like the canonical envelope.
func parseAPIError(body []byte, status int) *APIError {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var e APIError
	if err := json.Unmarshal(body, &e); err != nil {
		return nil
	}
	if e.Code == 0 && e.Message == "" {
		return nil
	}
	e.HTTPStatus = status
	return &e
}

// IsAlreadyExists reports whether err is a Cloud.ru "already exists" (gRPC code 6).
// Callers (Present) treat this as success for idempotency.
func IsAlreadyExists(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == ErrCodeAlreadyExists
}

// IsNotFound reports whether err is a Cloud.ru "not found" / "precondition"
// (gRPC code 5 or 9). CleanUp treats these as success.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == ErrCodeNotFound || apiErr.Code == ErrCodeFailedPrecondition
}
