package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestQuery_Success(t *testing.T) {
	overrideQuery = func(statement string, params map[string]any, readonly bool) (rows []map[string]any, metadata map[string]any, err error) {
		return []map[string]any{{"name": "Alice"}}, map[string]any{"metrics": map[string]any{"elapsed_time": "1ms", "result_count": 1}}, nil
	}
	t.Cleanup(func() { overrideQuery = nil })

	r := chi.NewRouter()
	r.Post("/query", QueryHandler(nil, nil))
	body := bytes.NewBufferString(`{"statement":"SELECT 1 WHERE 1=$one","params":{"one":1}}`)
	req := httptest.NewRequest(http.MethodPost, "/query", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestQuery_RejectNonParameterized(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/query", QueryHandler(nil, nil))
	body := bytes.NewBufferString(`{"statement":"SELECT 1"}`)
	req := httptest.NewRequest(http.MethodPost, "/query", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
