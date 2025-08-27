package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func setupRouter() *chi.Mux {
	r := chi.NewRouter()
	return r
}

func TestGetDocument_Success(t *testing.T) {
	overrideGet = func(bucket, scope, collection, id string) (cas uint64, content map[string]any, err error) {
		return 123, map[string]any{"name": "Alice"}, nil
	}
	t.Cleanup(func() { overrideGet = nil })

	r := setupRouter()
	r.Get("/buckets/{bucket}/docs/{id}", GetDocument(nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/buckets/users/docs/user::1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestGetDocument_NotFound(t *testing.T) {
	overrideGet = func(bucket, scope, collection, id string) (cas uint64, content map[string]any, err error) {
		return 0, nil, errors.New("document not found")
	}
	t.Cleanup(func() { overrideGet = nil })

	r := setupRouter()
	r.Get("/buckets/{bucket}/docs/{id}", GetDocument(nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/buckets/users/docs/user::missing", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestCreateDocument_Success(t *testing.T) {
	overrideInsert = func(bucket, scope, collection, id string, body json.RawMessage) (cas uint64, err error) {
		return 987, nil
	}
	t.Cleanup(func() { overrideInsert = nil })

	r := setupRouter()
	r.Post("/buckets/{bucket}/docs", CreateDocument(nil, nil))
	body := bytes.NewBufferString(`{"name":"Alice"}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
}

func TestUpsertDocument_Success(t *testing.T) {
	overrideUpsert = func(bucket, scope, collection, id string, body json.RawMessage) (cas uint64, err error) {
		return 111, nil
	}
	t.Cleanup(func() { overrideUpsert = nil })

	r := setupRouter()
	r.Put("/buckets/{bucket}/docs/{id}", UpsertDocument(nil, nil))
	body := bytes.NewBufferString(`{"name":"Bob"}`)
	req := httptest.NewRequest(http.MethodPut, "/buckets/users/docs/user::2", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestDeleteDocument_Success(t *testing.T) {
	overrideDelete = func(bucket, scope, collection, id string) (cas uint64, err error) {
		return 222, nil
	}
	t.Cleanup(func() { overrideDelete = nil })

	r := setupRouter()
	r.Delete("/buckets/{bucket}/docs/{id}", DeleteDocument(nil, nil))
	req := httptest.NewRequest(http.MethodDelete, "/buckets/users/docs/user::3", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
