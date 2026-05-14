package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	legowait "github.com/go-acme/lego/v5/internal/wait"
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
// the operation's Done flag flips to true or OperationTimeout elapses.
//
// The returned Operation is the final state. If the operation failed remotely
// (op.Error != nil) the function still returns the Operation along with a
// non-nil error so the caller can inspect the resourceId.
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

	var result *Operation
	err := legowait.For(timeout, interval, func() (bool, error) {
		op, err := c.GetOperation(ctx, opID)
		if err != nil {
			return false, err
		}
		if !op.Done {
			return false, nil
		}
		result = op
		if op.Error != nil {
			return true, fmt.Errorf("operation %s failed: code=%d %s",
				opID, op.Error.Code, op.Error.Message)
		}
		return true, nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}
