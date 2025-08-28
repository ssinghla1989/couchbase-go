package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func setupRouterMeta() *chi.Mux {
	r := chi.NewRouter()
	return r
}

func TestHEAD_Exists_SetsETag(t *testing.T) {
	overrideExists = func(bucket, scope, collection, id string) (bool, uint64, error) {
		return true, 555, nil
	}
	t.Cleanup(func() { overrideExists = nil })

	r := setupRouterMeta()
	r.Head("/buckets/{bucket}/docs/{id}", HeadDocument(nil, nil))
	req := httptest.NewRequest(http.MethodHead, "/buckets/users/docs/user::a", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"555\"" {
		t.Fatalf("expected ETag \"555\", got %q", got)
	}
}

func TestHEAD_NotFound_NoBody(t *testing.T) {
	overrideExists = func(bucket, scope, collection, id string) (bool, uint64, error) {
		return false, 0, nil
	}
	t.Cleanup(func() { overrideExists = nil })

	r := setupRouterMeta()
	r.Head("/buckets/{bucket}/docs/{id}", HeadDocument(nil, nil))
	req := httptest.NewRequest(http.MethodHead, "/buckets/users/docs/missing", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("expected empty body for HEAD 404")
	}
}

func TestGET_Meta_WithExpiry(t *testing.T) {
	overrideGetMeta = func(bucket, scope, collection, id string) (uint64, *time.Time, bool, error) {
		cas := uint64(777)
		exp := time.Now().Add(5 * time.Minute).UTC()
		return cas, &exp, true, nil
	}
	t.Cleanup(func() { overrideGetMeta = nil })

	r := setupRouterMeta()
	r.Get("/buckets/{bucket}/docs/{id}/meta", GetDocumentMeta(nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/buckets/users/docs/user::a/meta", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"777\"" {
		t.Fatalf("expected ETag \"777\", got %q", got)
	}
}

func TestGET_Meta_NoExpiry(t *testing.T) {
	overrideGetMeta = func(bucket, scope, collection, id string) (uint64, *time.Time, bool, error) {
		cas := uint64(888)
		return cas, nil, true, nil
	}
	t.Cleanup(func() { overrideGetMeta = nil })

	r := setupRouterMeta()
	r.Get("/buckets/{bucket}/docs/{id}/meta", GetDocumentMeta(nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/buckets/users/docs/user::b/meta", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestGET_Meta_NotFound(t *testing.T) {
	overrideGetMeta = func(bucket, scope, collection, id string) (uint64, *time.Time, bool, error) {
		return 0, nil, false, nil
	}
	t.Cleanup(func() { overrideGetMeta = nil })

	r := setupRouterMeta()
	r.Get("/buckets/{bucket}/docs/{id}/meta", GetDocumentMeta(nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/buckets/users/docs/missing/meta", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}
