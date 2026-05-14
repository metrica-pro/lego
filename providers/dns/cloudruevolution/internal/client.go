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
	"github.com/go-acme/lego/v5/log"
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
	rawBody, err := encodeRequestBody(reqBody)
	if err != nil {
		return err
	}

	endpoint, err := c.buildURL(path, query)
	if err != nil {
		return err
	}

	for attempt := range 2 {
		retry, err := c.attempt(ctx, method, endpoint, rawBody, respOut, attempt)
		if !retry {
			return err
		}
	}
	// Both attempts hit 401 — final attempt's handler already returned, so
	// reaching here is unexpected.
	return errors.New("cloudruevolution: 401 after retry")
}

// attempt performs a single iteration of do. retry=true means the caller
// should loop again; retry=false means err is the final outcome.
func (c *Client) attempt(ctx context.Context, method, endpoint string, rawBody []byte, respOut any, attempt int) (bool, error) {
	req, err := c.newRequest(ctx, method, endpoint, rawBody)
	if err != nil {
		return false, err
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false, errutils.NewHTTPDoError(req, err)
	}

	body, err := io.ReadAll(resp.Body)

	_ = resp.Body.Close()
	if err != nil {
		return false, errutils.NewReadResponseError(req, resp.StatusCode, err)
	}

	// Retry once on 401: server rejected our token. Drop the cache so the
	// next newRequest fetches a fresh one. Log a truncated body at debug
	// level so operators can see IAM-side reasons (e.g. "key revoked") when
	// the second attempt still fails.
	if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
		log.Debugf(log.LazySprintf("cloudruevolution: %s %s → 401, refreshing token (body: %q)",
			req.Method, req.URL.Path, truncate(body, 256)))
		c.identity.invalidate()

		return true, nil
	}

	return false, c.handleResponse(req, resp, body, respOut)
}

// handleResponse interprets the response body. 2xx with a sink unmarshals it;
// non-2xx maps to APIError when the envelope is recognized, otherwise to
// errutils.UnexpectedStatusCodeError.
func (c *Client) handleResponse(req *http.Request, resp *http.Response, body []byte, respOut any) error {
	if resp.StatusCode/100 != 2 {
		if apiErr := parseAPIError(body, resp.StatusCode); apiErr != nil {
			return apiErr
		}

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

// encodeRequestBody marshals reqBody when non-nil; otherwise returns (nil, nil).
func encodeRequestBody(reqBody any) ([]byte, error) {
	if reqBody == nil {
		return nil, nil
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	return raw, nil
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
	if json.Unmarshal(body, &e) != nil {
		return nil //nolint:nilerr // unparseable body is intentionally a non-envelope signal
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

// truncate returns body cut to at most n bytes, with a "…" indicator when
// truncated. Used for short debug log excerpts.
func truncate(body []byte, n int) string {
	if len(body) <= n {
		return string(body)
	}

	return string(body[:n]) + "…"
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
