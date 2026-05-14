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
	"sync"
	"time"

	"github.com/go-acme/lego/v5/internal/errutils"
	"github.com/go-acme/lego/v5/internal/useragent"
)

// tokenRefreshThreshold is the safety margin: if a cached token expires in
// less than this duration, getToken refreshes it eagerly.
const tokenRefreshThreshold = 60 * time.Second

// refreshThresholdFor picks a refresh margin that never exceeds half the
// reported expires_in. With expires_in=30s this returns 15s, so cached
// tokens still get reused across rapid-fire callers rather than every
// getToken hitting IAM.
func refreshThresholdFor(expiresIn time.Duration) time.Duration {
	if expiresIn <= 0 {
		return tokenRefreshThreshold
	}

	if half := expiresIn / 2; half < tokenRefreshThreshold {
		return half
	}

	return tokenRefreshThreshold
}

// identity holds the credentials and the cached bearer token.
// Access is guarded by RWMutex so concurrent callers can share a valid token
// without serializing on the IAM endpoint.
type identity struct {
	keyID, secret string
	authURL       *url.URL
	httpClient    *http.Client

	mu    sync.RWMutex
	token *Token
}

func newIdentity(keyID, secret string, authURL *url.URL, httpClient *http.Client) *identity {
	return &identity{
		keyID:      keyID,
		secret:     secret,
		authURL:    authURL,
		httpClient: httpClient,
	}
}

// getToken returns a valid bearer token, refreshing it from the IAM endpoint
// if there is no cached token or the cached token is within
// tokenRefreshThreshold of expiry.
func (i *identity) getToken(ctx context.Context) (*Token, error) {
	i.mu.RLock()
	cached := i.token
	i.mu.RUnlock()

	if cached.usableThreshold() {
		return cached, nil
	}

	i.mu.Lock()
	// Double-check after acquiring the write lock.
	if i.token.usableThreshold() {
		tok := i.token
		i.mu.Unlock()

		return tok, nil
	}
	i.mu.Unlock()

	// Release the lock while doing the HTTP roundtrip; concurrent callers
	// may end up performing redundant refreshes under heavy contention, but
	// readers are never blocked on the IAM endpoint.
	tok, err := i.obtainToken(ctx)
	if err != nil {
		return nil, err
	}

	i.mu.Lock()
	// Another caller may have populated a newer token in the meantime; keep
	// whichever expires later.
	if i.token == nil || tok.ExpiresAt.After(i.token.ExpiresAt) {
		i.token = tok
	} else {
		tok = i.token
	}
	i.mu.Unlock()

	return tok, nil
}

// invalidate drops the cached token so the next getToken forces a refresh.
// Called by the HTTP layer after a 401 response.
func (i *identity) invalidate() {
	i.mu.Lock()
	i.token = nil
	i.mu.Unlock()
}

// obtainToken performs the IAM POST and returns a populated Token.
// Body is JSON {"keyId":"...","secret":"..."}.
// Caller must hold no lock — this method is HTTP only.
func (i *identity) obtainToken(ctx context.Context) (*Token, error) {
	body, err := json.Marshal(AuthRequest{KeyID: i.keyID, Secret: i.secret})
	if err != nil {
		return nil, fmt.Errorf("auth: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.authURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("auth: new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	useragent.SetHeader(req.Header)

	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, errutils.NewHTTPDoError(req, err)
	}

	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errutils.NewReadResponseError(req, resp.StatusCode, err)
	}

	if resp.StatusCode/100 != 2 {
		// Reconstruct the response shape errutils expects.
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		return nil, errutils.NewUnexpectedResponseStatusCodeError(req, resp)
	}

	var tok Token
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return nil, errutils.NewUnmarshalError(req, resp.StatusCode, respBody, err)
	}

	if tok.AccessToken == "" {
		return nil, errors.New("auth: empty access_token in response")
	}

	expiresIn := tok.ExpiresIn
	if expiresIn <= 0 {
		// Fall back to a conservative one-hour TTL if the IAM payload omits it.
		expiresIn = 3600
	}

	// Cloud.ru's IAM normally returns ExpiresIn=3600 but a misconfiguration
	// or a short-lived test token can produce something well below the
	// refresh threshold. Without clamping every getToken would issue a fresh
	// POST and effectively DoS the IAM endpoint when many challenges fire in
	// parallel. Pick a refresh threshold equal to half the TTL.
	tok.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)

	return &tok, nil
}
