package couchbase

import (
	"context"
	"time"

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
		Username: cfg.Couchbase.Username,
		Password: cfg.Couchbase.Password,
		TimeoutsConfig: gocb.TimeoutsConfig{
			KVTimeout:      time.Duration(cfg.Couchbase.KVOpsTimeout) * time.Millisecond,
			ConnectTimeout: time.Duration(cfg.Couchbase.KVDialTimeout) * time.Millisecond,
		},
	}
	cluster, err := gocb.Connect(cfg.Couchbase.ConnStr, clusterOpts)
	if err != nil {
		return nil, err
	}

	connectTimeout := time.Duration(cfg.Couchbase.KVDialTimeout) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := cluster.WaitUntilReady(connectTimeout, &gocb.WaitUntilReadyOptions{Context: ctx}); err != nil {
		_ = cluster.Close(nil)
		return nil, err
	}

	if logger != nil {
		logger.Info("connected to Couchbase", zap.String("bucket", cfg.Couchbase.Bucket))
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
	return c.cluster.Bucket(c.cfg.Couchbase.Bucket)
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

// WaitUntilReady waits until the underlying cluster is ready or the timeout elapses.
func (c *Client) WaitUntilReady(ctx context.Context, timeout time.Duration) error {
	// Intentionally ignore ctx to match requirement of calling WaitUntilReady(timeout, nil)
	_ = ctx
	if c == nil || c.cluster == nil {
		return context.Canceled
	}
	return c.cluster.WaitUntilReady(timeout, nil)
}

// ConnStr returns the configured cluster connection string.
func (c *Client) ConnStr() string {
	if c == nil || c.cfg == nil {
		return ""
	}
	return c.cfg.Couchbase.ConnStr
}

// Bucket returns a handle to a bucket by name.
func (c *Client) Bucket(name string) *gocb.Bucket {
	return c.cluster.Bucket(name)
}

// Cluster exposes the underlying gocb.Cluster.
func (c *Client) Cluster() *gocb.Cluster {
	return c.cluster
}

// Config returns the application configuration used to initialize the client.
func (c *Client) Config() *config.Config {
	return c.cfg
}
