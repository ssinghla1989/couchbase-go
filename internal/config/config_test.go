package config

import (
	"os"
	"testing"
)

func TestLoad_DefaultsAndValidation(t *testing.T) {
	// Set minimal required envs
	os.Setenv("CB_CONN_STR", "couchbase://localhost")
	os.Setenv("CB_USERNAME", "Administrator")
	os.Setenv("CB_PASSWORD", "password")
	defer func() {
		os.Unsetenv("CB_CONN_STR")
		os.Unsetenv("CB_USERNAME")
		os.Unsetenv("CB_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.ServerPort == 0 {
		t.Fatalf("expected default ServerPort to be set, got 0")
	}
	if cfg.Couchbase.Bucket == "" {
		t.Fatalf("expected default Couchbase.Bucket to be set")
	}
}
