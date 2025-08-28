package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func setupTTLRouter() *chi.Mux {
	r := chi.NewRouter()
	return r
}

func TestTouch_Valid(t *testing.T) {
	overrideTouch = func(bucket, scope, collection, id string, ttlSeconds int64) (uint64, error) {
		if ttlSeconds != 3600 {
			t.Fatalf("expected ttl 3600, got %d", ttlSeconds)
		}
		return 1234, nil
	}
	t.Cleanup(func() { overrideTouch = nil })

	r := setupTTLRouter()
	r.Post("/buckets/{bucket}/docs/{id}/touch", TouchDocument(nil, nil))
	body := bytes.NewBufferString(`{"ttl_seconds":3600, "scope":"app", "collection":"profiles"}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/user::1/touch", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"1234\"" {
		t.Fatalf("expected ETag \"1234\", got %q", got)
	}
}

func TestTouch_InvalidTTL(t *testing.T) {
	r := setupTTLRouter()
	r.Post("/buckets/{bucket}/docs/{id}/touch", TouchDocument(nil, nil))
	body := bytes.NewBufferString(`{"ttl_seconds":0}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/user::1/touch", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestTouch_NotFound(t *testing.T) {
	overrideTouch = func(bucket, scope, collection, id string, ttlSeconds int64) (uint64, error) {
		return 0, assertError("not found")
	}
	t.Cleanup(func() { overrideTouch = nil })

	r := setupTTLRouter()
	r.Post("/buckets/{bucket}/docs/{id}/touch", TouchDocument(nil, nil))
	body := bytes.NewBufferString(`{"ttl_seconds":10}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/missing/touch", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// assertError helps tests craft a string error quickly
type assertError string

func (e assertError) Error() string { return string(e) }

func TestTouch_NegativeTTL(t *testing.T) {
	r := setupTTLRouter()
	r.Post("/buckets/{bucket}/docs/{id}/touch", TouchDocument(nil, nil))
	body := bytes.NewBufferString(`{"ttl_seconds":-5}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/user::2/touch", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestTouch_TimeoutReturns503(t *testing.T) {
	overrideTouch = func(bucket, scope, collection, id string, ttlSeconds int64) (uint64, error) {
		return 0, assertError("timeout")
	}
	t.Cleanup(func() { overrideTouch = nil })

	r := setupTTLRouter()
	r.Post("/buckets/{bucket}/docs/{id}/touch", TouchDocument(nil, nil))
	body := bytes.NewBufferString(`{"ttl_seconds":5}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/user::t/touch", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
}
