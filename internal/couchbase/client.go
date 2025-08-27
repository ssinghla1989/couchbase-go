package couchbase

import (
	"context"

	"github.com/couchbase/gocb/v2"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/config"
)

// Client wraps the Couchbase SDK cluster and configuration.
type Client struct {
	cluster *gocb.Cluster
	cfg     *config.Config
	logger  *zap.Logger
}

// NewClient connects to Couchbase using the provided config and logger.
func NewClient(cfg *config.Config, logger *zap.Logger) (*Client, error) {
	clusterOpts := gocb.ClusterOptions{
		Username: cfg.CBUsername,
		Password: cfg.CBPassword,
	}
	cluster, err := gocb.Connect(cfg.CBConnStr, clusterOpts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.CBTimeout)
	defer cancel()
	if err := cluster.WaitUntilReady(cfg.CBTimeout, &gocb.WaitUntilReadyOptions{Context: ctx}); err != nil {
		_ = cluster.Close(nil)
		return nil, err
	}

	if logger != nil {
		logger.Info("connected to Couchbase", zap.String("bucket", cfg.CBBucket))
	}

	return &Client{cluster: cluster, cfg: cfg, logger: logger}, nil
}

// Close shuts down the Couchbase cluster connection.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.cluster == nil {
		return nil
	}
	// gocb v2.11 Close does not accept context; use default behavior.
	return c.cluster.Close(nil)
}

// GetBucket returns a handle to the configured bucket.
func (c *Client) GetBucket() *gocb.Bucket {
	return c.cluster.Bucket(c.cfg.CBBucket)
}

// GetCollection returns a handle to a collection within the configured bucket.
// This is a placeholder with no direct KV operations implemented.
func (c *Client) GetCollection(scopeName, collectionName string) *gocb.Collection {
	bucket := c.GetBucket()
	if scopeName == "" {
		return bucket.DefaultCollection()
	}
	return bucket.Scope(scopeName).Collection(collectionName)
}
