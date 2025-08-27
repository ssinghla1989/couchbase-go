package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
)

func setupRouterCAS() *chi.Mux {
	r := chi.NewRouter()
	return r
}

func TestGET_SetsETag(t *testing.T) {
	overrideGet = func(bucket, scope, collection, id string) (uint64, map[string]any, error) {
		return 12345, map[string]any{"name": "Alice"}, nil
	}
	t.Cleanup(func() { overrideGet = nil })

	r := setupRouterCAS()
	r.Get("/buckets/{bucket}/docs/{id}", GetDocument(nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/buckets/users/docs/user::1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"12345\"" {
		t.Fatalf("expected ETag \"12345\", got %q", got)
	}
}

func TestPUT_Replace_WithMatchingCAS(t *testing.T) {
	overrideReplaceCAS = func(bucket, scope, collection, id string, cas uint64, body json.RawMessage) (uint64, error) {
		return 22222, nil
	}
	t.Cleanup(func() { overrideReplaceCAS = nil })

	r := setupRouterCAS()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"Bob"}}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::2", body)
	req.Header.Set("If-Match", "\"11111\"")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"22222\"" {
		t.Fatalf("expected ETag \"22222\", got %q", got)
	}
}

func TestPUT_Replace_WithWrongCAS(t *testing.T) {
	overrideReplaceCAS = func(bucket, scope, collection, id string, cas uint64, body json.RawMessage) (uint64, error) {
		return 0, fmt.Errorf("cas mismatch")
	}
	t.Cleanup(func() { overrideReplaceCAS = nil })

	r := setupRouterCAS()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"Bob"}}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::2", body)
	req.Header.Set("If-Match", "\"11111\"")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
}

func TestPUT_CreateOnly_ExistsConflict(t *testing.T) {
	overrideInsert = func(bucket, scope, collection, id string, body json.RawMessage) (uint64, error) {
		return 0, fmt.Errorf("exists")
	}
	t.Cleanup(func() { overrideInsert = nil })

	r := setupRouterCAS()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"Bob"}}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::2", body)
	req.Header.Set("If-None-Match", "*")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
}

func TestPUT_CreateOnly_MissingDoc_Created(t *testing.T) {
	overrideInsert = func(bucket, scope, collection, id string, body json.RawMessage) (uint64, error) {
		return 33333, nil
	}
	t.Cleanup(func() { overrideInsert = nil })

	r := setupRouterCAS()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"Charlie"}}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::3", body)
	req.Header.Set("If-None-Match", "*")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"33333\"" {
		t.Fatalf("expected ETag \"33333\", got %q", got)
	}
}

func TestPUT_InvalidCASFormat_Returns400(t *testing.T) {
	r := setupRouterCAS()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"doc":{"name":"D"}}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::4", body)
	req.Header.Set("If-Match", "\"not-a-number\"")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestDELETE_WithMatchingCAS(t *testing.T) {
	overrideDeleteCAS = func(bucket, scope, collection, id string, cas uint64) (uint64, error) {
		return cas, nil
	}
	t.Cleanup(func() { overrideDeleteCAS = nil })

	r := setupRouterCAS()
	r.Delete("/buckets/{bucket}/docs/{id}", DeleteDocument(nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/buckets/users/docs/user::5", nil)
	req.Header.Set("If-Match", "\"777\"")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"777\"" {
		t.Fatalf("expected ETag \"777\", got %q", got)
	}
}

func TestDELETE_WrongCAS_Returns409(t *testing.T) {
	overrideDeleteCAS = func(bucket, scope, collection, id string, cas uint64) (uint64, error) {
		return 0, fmt.Errorf("mismatch")
	}
	t.Cleanup(func() { overrideDeleteCAS = nil })

	r := setupRouterCAS()
	r.Delete("/buckets/{bucket}/docs/{id}", DeleteDocument(nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/buckets/users/docs/user::6", nil)
	req.Header.Set("If-Match", "\"888\"")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
}

func TestDELETE_MissingCAS_WithRequireFlag_Returns428(t *testing.T) {
	// Set env to require CAS when config not provided
	os.Setenv("REQUIRE_CAS_ON_DELETE", "true")
	t.Cleanup(func() { os.Unsetenv("REQUIRE_CAS_ON_DELETE") })

	r := setupRouterCAS()
	r.Delete("/buckets/{bucket}/docs/{id}", DeleteDocument(nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/buckets/users/docs/user::7", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428, got %d", rr.Code)
	}
}
