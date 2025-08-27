package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLivenessHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/liveness", nil)
	rr := httptest.NewRecorder()

	LivenessHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}

func TestReadinessHandler_Healthy(t *testing.T) {
	// Stub readiness to succeed.
	readinessCheckOverride = func(_ context.Context, _ time.Duration) error { return nil }
	connStrOverride = func() string { return "couchbases://ipc1.prod.example.com" }
	t.Cleanup(func() {
		readinessCheckOverride = nil
		connStrOverride = nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/readiness", nil)
	rr := httptest.NewRecorder()

	h := ReadinessHandler(nil, nil)
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}

func TestReadinessHandler_Unhealthy(t *testing.T) {
	// Stub readiness to fail.
	readinessCheckOverride = func(_ context.Context, _ time.Duration) error {
		return errors.New("failed to connect to cluster: timeout")
	}
	t.Cleanup(func() { readinessCheckOverride = nil })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/readiness", nil)
	rr := httptest.NewRecorder()

	h := ReadinessHandler(nil, nil)
	h(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rr.Code)
	}
}
