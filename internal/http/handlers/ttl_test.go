package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func setupRouterTTL() *chi.Mux {
	r := chi.NewRouter()
	return r
}

func TestPOST_WithTTL_UsesOverrideWithExpiry(t *testing.T) {
	var capturedTTL int64
	overrideInsertWithExpiry = func(bucket, scope, collection, id string, body json.RawMessage, expirySeconds int64) (uint64, error) {
		capturedTTL = expirySeconds
		return 999, nil
	}
	t.Cleanup(func() { overrideInsertWithExpiry = nil })

	r := setupRouterTTL()
	r.Post("/buckets/{bucket}/docs", CreateDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"X"}, "ttl_seconds": 120}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	if capturedTTL != 120 {
		t.Fatalf("expected ttl 120, got %d", capturedTTL)
	}
}

func TestPUT_WithTTL_Upsert_UsesOverrideWithExpiry(t *testing.T) {
	var capturedTTL int64
	overrideUpsertWithExpiry = func(bucket, scope, collection, id string, body json.RawMessage, expirySeconds int64) (uint64, error) {
		capturedTTL = expirySeconds
		return 1234, nil
	}
	t.Cleanup(func() { overrideUpsertWithExpiry = nil })

	r := setupRouterTTL()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"Y"}, "ttl_seconds": 45}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::ttl1", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if capturedTTL != 45 {
		t.Fatalf("expected ttl 45, got %d", capturedTTL)
	}
}

func TestPUT_WithTTL_Replace_UsesOverrideWithExpiry(t *testing.T) {
	var capturedTTL int64
	overrideReplaceCASWithExpiry = func(bucket, scope, collection, id string, cas uint64, body json.RawMessage, expirySeconds int64) (uint64, error) {
		capturedTTL = expirySeconds
		return 4321, nil
	}
	t.Cleanup(func() { overrideReplaceCASWithExpiry = nil })

	r := setupRouterTTL()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"Z"}, "ttl_seconds": 3600}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::ttl2", body)
	req.Header.Set("If-Match", "\"1\"")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if capturedTTL != 3600 {
		t.Fatalf("expected ttl 3600, got %d", capturedTTL)
	}
}

func TestPOST_InvalidTTL_Negative_Returns400(t *testing.T) {
	r := setupRouterTTL()
	r.Post("/buckets/{bucket}/docs", CreateDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"a":1}, "ttl_seconds": -1}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestPOST_InvalidTTL_NonInteger_Returns400(t *testing.T) {
	r := setupRouterTTL()
	r.Post("/buckets/{bucket}/docs", CreateDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"a":1}, "ttl_seconds": 3.14}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestPOST_InvalidTTL_TooLarge_Returns400(t *testing.T) {
	r := setupRouterTTL()
	r.Post("/buckets/{bucket}/docs", CreateDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"a":1}, "ttl_seconds": 2147483648}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
