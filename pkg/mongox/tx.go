package mongox

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

// TxFunc is the function signature executed within a transaction.
// If TxFunc returns a non-nil error, the transaction is aborted; otherwise it is committed.
type TxFunc func(ctx context.Context) error

// RunTransaction executes fn within a MongoDB transaction using "snapshot" read concern
// and "majority" write concern for strong consistency guarantees.
// The session is automatically ended after the transaction completes.
func (c *Client) RunTransaction(ctx context.Context, fn TxFunc) error {
	session, err := c.raw.StartSession()
	if err != nil {
		return fmt.Errorf("mongox: start session failed: %w", err)
	}
	defer session.EndSession(ctx)

	txOpts := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())

	_, err = session.WithTransaction(ctx, func(sc context.Context) (any, error) {
		if txErr := fn(sc); txErr != nil {
			return nil, txErr
		}
		return nil, nil
	}, txOpts)

	if err != nil {
		return fmt.Errorf("mongox: transaction failed: %w", err)
	}
	return nil
}

// RunTransactionWithResult executes fn within a MongoDB transaction and returns a result.
// Uses "snapshot" read concern and "majority" write concern.
func RunTransactionWithResult[T any](c *Client, ctx context.Context, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T

	session, err := c.raw.StartSession()
	if err != nil {
		return zero, fmt.Errorf("mongox: start session failed: %w", err)
	}
	defer session.EndSession(ctx)

	txOpts := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())

	result, err := session.WithTransaction(ctx, func(sc context.Context) (any, error) {
		return fn(sc)
	}, txOpts)

	if err != nil {
		return zero, fmt.Errorf("mongox: transaction failed: %w", err)
	}

	typed, ok := result.(T)
	if !ok {
		return zero, fmt.Errorf("mongox: unexpected transaction result type")
	}
	return typed, nil
}

// IsTransactionError checks if an error is a MongoDB transient transaction error,
// which may be retried by the application.
func IsTransactionError(err error) bool {
	if err == nil {
		return false
	}
	var cmdErr mongo.CommandError
	if !errors.As(err, &cmdErr) {
		return false
	}
	return cmdErr.HasErrorLabel("TransientTransactionError")
}
