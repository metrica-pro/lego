package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// GetOperation fetches the current state of an asynchronous operation.
func (c *Client) GetOperation(ctx context.Context, opID string) (*Operation, error) {
	var op Operation
	if err := c.do(ctx, http.MethodGet, "/v1/operations/"+opID, nil, nil, &op); err != nil {
		return nil, fmt.Errorf("get operation %s: %w", opID, err)
	}

	return &op, nil
}

// WaitForOperation polls /v1/operations/{id} every OperationPollInterval until
// the operation's Done flag flips to true, OperationTimeout elapses, or ctx
// is canceled. The returned Operation is the final state observed; on
// remote failure (op.Error != nil) the operation is returned alongside a
// non-nil error so the caller can recover op.ResourceID for cleanup.
func (c *Client) WaitForOperation(ctx context.Context, opID string) (*Operation, error) {
	if opID == "" {
		return nil, errors.New("waitForOperation: empty operation id")
	}

	timeout := c.OperationTimeout
	if timeout <= 0 {
		timeout = DefaultOperationTimeout
	}

	interval := c.OperationPollInterval
	if interval <= 0 {
		interval = DefaultOperationPollInterval
	}

	deadline := time.Now().Add(timeout)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastOp *Operation

	for {
		op, err := c.GetOperation(ctx, opID)
		if err != nil {
			return lastOp, err
		}

		lastOp = op

		// Cloud.ru may emit a terminal error before flipping Done. Short-
		// circuit so the caller observes the failure promptly.
		if op.Error != nil {
			return op, fmt.Errorf("operation %s failed: code=%d %s",
				opID, op.Error.Code, op.Error.Message)
		}

		if op.Done {
			return op, nil
		}

		if time.Now().After(deadline) {
			return op, fmt.Errorf("operation %s: timeout after %s (last state: done=%v)",
				opID, timeout, op.Done)
		}

		select {
		case <-ctx.Done():
			return op, fmt.Errorf("operation %s: context canceled: %w", opID, ctx.Err())
		case <-ticker.C:
		}
	}
}
