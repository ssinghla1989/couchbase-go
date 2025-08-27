package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func setupBulkRouter() *chi.Mux {
	r := chi.NewRouter()
	return r
}

func TestBulkGet_ValidationErrors(t *testing.T) {
	r := setupBulkRouter()
	r.Post("/buckets/{bucket}/docs/_bulk_get", BulkGet(nil, nil))

	// empty ids
	body := bytes.NewBufferString(`{"ids":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/_bulk_get", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestBulkUpsert_ValidationErrors(t *testing.T) {
	r := setupBulkRouter()
	r.Post("/buckets/{bucket}/docs/_bulk_upsert", BulkUpsert(nil, nil))

	// empty items
	body := bytes.NewBufferString(`{"items":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/_bulk_upsert", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	// bad durability
	body = bytes.NewBufferString(`{"items":[{"id":"a","doc":{}}],"durability":"foo"}`)
	req = httptest.NewRequest(http.MethodPost, "/buckets/users/docs/_bulk_upsert", body)
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestBulkGet_MixedResults(t *testing.T) {
	overrideBulkGet = func(bucket, scope, collection string, ids []string) ([]bulkGetItemResult, error) {
		return []bulkGetItemResult{
			{ID: "user::1", OK: true, CAS: "123", Content: map[string]any{"name": "A"}},
			{ID: "user::2", OK: false, Error: &bulkError{Code: "not_found", Message: "document not found"}},
		}, nil
	}
	t.Cleanup(func() { overrideBulkGet = nil })

	r := setupBulkRouter()
	r.Post("/buckets/{bucket}/docs/_bulk_get", BulkGet(nil, nil))
	reqBody, _ := json.Marshal(bulkGetRequest{IDs: []string{"user::1", "user::2"}})
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/_bulk_get", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestBulkUpsert_MixedResults(t *testing.T) {
	overrideBulkUpsert = func(bucket, scope, collection, durability string, items []bulkUpsertItem) ([]bulkUpsertItemResult, error) {
		return []bulkUpsertItemResult{
			{ID: "user::1", OK: true, CAS: "123"},
			{ID: "user::2", OK: false, Error: &bulkError{Code: "invalid", Message: "doc must be an object"}},
		}, nil
	}
	t.Cleanup(func() { overrideBulkUpsert = nil })

	r := setupBulkRouter()
	r.Post("/buckets/{bucket}/docs/_bulk_upsert", BulkUpsert(nil, nil))
	reqBody, _ := json.Marshal(bulkUpsertRequest{Items: []bulkUpsertItem{{ID: "user::1", Doc: json.RawMessage(`{"name":"A"}`)}, {ID: "user::2", Doc: json.RawMessage(`{"name":"B"}`)}}})
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/_bulk_upsert", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestBulkHandlers_ClusterUnreachable(t *testing.T) {
	overrideBulkReady = func(timeout time.Duration) error { return errors.New("cluster unreachable") }
	t.Cleanup(func() { overrideBulkReady = nil })

	r := setupBulkRouter()
	r.Post("/buckets/{bucket}/docs/_bulk_get", BulkGet(nil, nil))
	r.Post("/buckets/{bucket}/docs/_bulk_upsert", BulkUpsert(nil, nil))

	// bulk get
	reqBody, _ := json.Marshal(bulkGetRequest{IDs: []string{"a"}})
	req := httptest.NewRequest(http.MethodPost, "/buckets/users/docs/_bulk_get", bytes.NewReader(reqBody))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}

	// bulk upsert
	reqBody, _ = json.Marshal(bulkUpsertRequest{Items: []bulkUpsertItem{{ID: "a", Doc: json.RawMessage(`{"x":1}`)}}})
	req = httptest.NewRequest(http.MethodPost, "/buckets/users/docs/_bulk_upsert", bytes.NewReader(reqBody))
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
}
