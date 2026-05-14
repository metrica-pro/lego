package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RecordTypeTXT is the value Cloud.ru expects for TXT records. The API
// requires lowercase types.
const RecordTypeTXT = "txt"

// ListRecords returns all records (any type) in the given public zone.
// Pagination is followed transparently.
func (c *Client) ListRecords(ctx context.Context, zoneID string) ([]PublicRecord, error) {
	q := url.Values{}
	q.Set("publicZoneId", zoneID)

	var all []PublicRecord

	for {
		var page ListRecordsResponse
		if err := c.do(ctx, http.MethodGet, "/v1/publicRecordsSole", q, nil, &page); err != nil {
			return nil, fmt.Errorf("list records in zone %s: %w", zoneID, err)
		}

		all = append(all, page.Records...)
		if page.NextPageToken == "" {
			break
		}

		q.Set("pageToken", page.NextPageToken)
	}

	return all, nil
}

// FindTXTRecord searches a zone for a TXT record with the given name (relative
// to the zone, e.g. "_acme-challenge"). Returns nil if not found.
func (c *Client) FindTXTRecord(ctx context.Context, zoneID, name string) (*PublicRecord, error) {
	records, err := c.ListRecords(ctx, zoneID)
	if err != nil {
		return nil, err
	}

	for i := range records {
		r := &records[i]
		if !strings.EqualFold(r.Type, RecordTypeTXT) {
			continue
		}

		if r.Name == name {
			return r, nil
		}
	}

	return nil, nil
}

// GetRecord fetches a single record by ID.
func (c *Client) GetRecord(ctx context.Context, recordID string) (*PublicRecord, error) {
	var r PublicRecord
	if err := c.do(ctx, http.MethodGet, "/v1/publicRecordsSole/"+recordID, nil, nil, &r); err != nil {
		return nil, fmt.Errorf("get record %s: %w", recordID, err)
	}

	return &r, nil
}

// CreateRecord submits a new record and returns the Operation envelope. The
// caller should usually invoke WaitForOperation to receive the final state.
func (c *Client) CreateRecord(ctx context.Context, req CreateRecordRequest) (*Operation, error) {
	var op Operation
	if err := c.do(ctx, http.MethodPost, "/v1/publicRecordsSole", nil, req, &op); err != nil {
		return nil, err
	}

	return &op, nil
}

// UpdateRecord replaces the mutable fields of an existing record.
func (c *Client) UpdateRecord(ctx context.Context, recordID string, req UpdateRecordRequest) (*Operation, error) {
	var op Operation
	if err := c.do(ctx, http.MethodPatch, "/v1/publicRecordsSole/"+recordID, nil, req, &op); err != nil {
		return nil, err
	}

	return &op, nil
}

// DeleteRecord removes a record by ID.
func (c *Client) DeleteRecord(ctx context.Context, recordID string) (*Operation, error) {
	var op Operation
	if err := c.do(ctx, http.MethodDelete, "/v1/publicRecordsSole/"+recordID, nil, nil, &op); err != nil {
		return nil, err
	}

	return &op, nil
}

// CreateRecordAndWait posts a new record and blocks until the resulting
// operation finishes. Returns the resourceId of the freshly created record.
//
// On wait timeout / context cancellation the partial Operation envelope is
// returned alongside the error so the caller can still see the resourceId
// and either retry or schedule a CleanUp — the record may have been
// committed server-side after the wait deadline expired.
func (c *Client) CreateRecordAndWait(ctx context.Context, req CreateRecordRequest) (string, error) {
	op, err := c.CreateRecord(ctx, req)
	if err != nil {
		return "", err
	}

	final, waitErr := c.WaitForOperation(ctx, op.ID)
	if waitErr != nil {
		// Surface the resourceId from whichever envelope is non-nil so the
		// orchestrator can resume cleanup against the stranded record.
		stranded := op.ResourceID
		if final != nil && final.ResourceID != "" {
			stranded = final.ResourceID
		}

		if stranded != "" {
			return stranded, fmt.Errorf("create record %s: %w", stranded, waitErr)
		}

		return "", waitErr
	}

	return final.ResourceID, nil
}

// UpdateRecordAndWait patches a record and waits for the operation to finish.
func (c *Client) UpdateRecordAndWait(ctx context.Context, recordID string, req UpdateRecordRequest) error {
	op, err := c.UpdateRecord(ctx, recordID, req)
	if err != nil {
		return err
	}

	_, err = c.WaitForOperation(ctx, op.ID)

	return err
}

// DeleteRecordAndWait deletes a record and waits for the operation to finish.
// HTTP 400 with gRPC code 9 (not found / precondition) is treated as success
// for callers driving idempotent cleanup paths.
func (c *Client) DeleteRecordAndWait(ctx context.Context, recordID string) error {
	op, err := c.DeleteRecord(ctx, recordID)
	if err != nil {
		if IsNotFound(err) {
			return nil
		}

		return err
	}

	_, err = c.WaitForOperation(ctx, op.ID)

	return err
}
