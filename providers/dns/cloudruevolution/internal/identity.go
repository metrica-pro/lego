package internal

import (
	"net/http"
	"net/url"
	"sync"
)

// identity holds the credentials and the cached bearer token.
// It is filled in fully in a later commit; here it exists so the skeleton compiles.
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
