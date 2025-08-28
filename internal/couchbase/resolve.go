package couchbase

import (
	"context"
	"time"

	"github.com/couchbase/gocb/v2"
)

// ResolveCollection returns a collection handle after ensuring the bucket is ready.
// Empty scope/collection default to _default.
func ResolveCollection(cb *Client, bucketName, scopeName, collectionName string, ctx context.Context) (*gocb.Collection, error) {
	if cb == nil || cb.Cluster() == nil {
		return nil, context.Canceled
	}
	if bucketName == "" {
		return nil, context.Canceled
	}
	if scopeName == "" {
		scopeName = "_default"
	}
	if collectionName == "" {
		collectionName = "_default"
	}
	bucket := cb.Bucket(bucketName)
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := bucket.WaitUntilReady(2*time.Second, &gocb.WaitUntilReadyOptions{Context: readyCtx}); err != nil {
		return nil, err
	}
	return bucket.Scope(scopeName).Collection(collectionName), nil
}
