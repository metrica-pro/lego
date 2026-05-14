// Package internal contains the HTTP client and types for the Cloud.ru Evolution DNS API.
package internal

import (
	"fmt"
	"time"
)

// Token represents an OAuth2 bearer token obtained from the Cloud.ru IAM endpoint.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`

	// ExpiresAt is the absolute deadline computed when the token is fetched.
	ExpiresAt time.Time `json:"-"`
}

// Valid reports whether the token can still be used safely.
// The threshold is subtracted from the deadline to allow proactive refresh.
func (t *Token) Valid(threshold time.Duration) bool {
	return t != nil && t.AccessToken != "" && time.Until(t.ExpiresAt) > threshold
}

// AuthRequest is the JSON body sent to the IAM token endpoint.
type AuthRequest struct {
	KeyID  string `json:"keyId"`
	Secret string `json:"secret"`
}

// PublicZone is a public DNS zone (Cloud.ru Evolution DNS).
type PublicZone struct {
	ID                string   `json:"id"`
	ProjectID         string   `json:"projectId"`
	Name              string   `json:"name"`
	Domain            string   `json:"domain"`
	State             string   `json:"state,omitempty"`
	ConfirmState      string   `json:"confirmState,omitempty"`
	Description       string   `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	VerificationToken string   `json:"verificationToken,omitempty"`
}

// PublicRecord is a DNS record stored in a public zone.
type PublicRecord struct {
	ID           string   `json:"id,omitempty"`
	PublicZoneID string   `json:"publicZoneId,omitempty"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Values       []string `json:"values"`
	TTL          int      `json:"ttl"`
	Description  string   `json:"description,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// CreateRecordRequest is the JSON body for POST /v1/publicRecordsSole.
type CreateRecordRequest struct {
	PublicZoneID string   `json:"publicZoneId"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Values       []string `json:"values"`
	TTL          int      `json:"ttl"`
}

// UpdateRecordRequest is the JSON body for PATCH /v1/publicRecordsSole/{id}.
type UpdateRecordRequest struct {
	Values []string `json:"values"`
	TTL    int      `json:"ttl"`
}

// Operation represents an asynchronous Cloud.ru DNS operation.
// POST, PATCH and DELETE on resources return one; the caller polls
// /v1/operations/{id} until Done is true.
type Operation struct {
	ID           string        `json:"id"`
	ResourceID   string        `json:"resourceId,omitempty"`
	ResourceName string        `json:"resourceName,omitempty"`
	Done         bool          `json:"done"`
	Description  string        `json:"description,omitempty"`
	Error        *OperationErr `json:"error,omitempty"`
}

// OperationErr carries the error payload of a failed operation.
type OperationErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ListZonesResponse is the body of GET /v1/publicZones.
type ListZonesResponse struct {
	Zones         []PublicZone `json:"zones"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

// ListRecordsResponse is the body of GET /v1/publicRecordsSole.
type ListRecordsResponse struct {
	Records       []PublicRecord `json:"records"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

// APIError is the canonical error envelope returned by the Cloud.ru DNS API.
// Code mirrors gRPC status codes — see the well-known constants below.
type APIError struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Details []APIErrorDetail `json:"details,omitempty"`

	// HTTPStatus is filled by the client for callers that need it.
	HTTPStatus int `json:"-"`
}

// APIErrorDetail is one entry of the polymorphic details array.
type APIErrorDetail struct {
	Type            string           `json:"@type,omitempty"`
	Description     string           `json:"description,omitempty"`
	FieldViolations []FieldViolation `json:"fieldViolations,omitempty"`
	ResourceType    string           `json:"resourceType,omitempty"`
	ResourceName    string           `json:"resourceName,omitempty"`
	Owner           string           `json:"owner,omitempty"`
}

// FieldViolation describes a single validation failure.
type FieldViolation struct {
	Field       string `json:"field"`
	Description string `json:"description"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cloudruevolution: API error code=%d http=%d: %s", e.Code, e.HTTPStatus, e.Message)
}

// gRPC status codes used by the Cloud.ru DNS API.
const (
	ErrCodeInvalidArgument    = 3 // validation
	ErrCodeNotFound           = 5 // route not found (does not happen for valid endpoints)
	ErrCodeAlreadyExists      = 6 // duplicate resource (POST returns 409)
	ErrCodeFailedPrecondition = 9 // resource missing (DELETE/GET on deleted record returns 400)
)
