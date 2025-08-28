package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func setupCountersRouter() *chi.Mux {
	r := chi.NewRouter()
	return r
}

func TestCounters_Increment(t *testing.T) {
	overrideCounter = func(bucket, scope, collection, id string, delta int64, initial *uint64, ttlSeconds *uint32) (uint64, uint64, error) {
		if delta != 5 {
			t.Fatalf("expected delta 5, got %d", delta)
		}
		return 42, 999, nil
	}
	t.Cleanup(func() { overrideCounter = nil })

	r := setupCountersRouter()
	r.Post("/buckets/{bucket}/counters/{id}", CounterOp(nil, nil))
	body := bytes.NewBufferString(`{"delta":5}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/metrics/counters/c1", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != "\"999\"" {
		t.Fatalf("expected ETag \"999\", got %q", got)
	}
}

func TestCounters_Decrement(t *testing.T) {
	overrideCounter = func(bucket, scope, collection, id string, delta int64, initial *uint64, ttlSeconds *uint32) (uint64, uint64, error) {
		if delta != -2 {
			t.Fatalf("expected delta -2, got %d", delta)
		}
		return 40, 111, nil
	}
	t.Cleanup(func() { overrideCounter = nil })

	r := setupCountersRouter()
	r.Post("/buckets/{bucket}/counters/{id}", CounterOp(nil, nil))
	body := bytes.NewBufferString(`{"delta":-2}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/metrics/counters/c2", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestCounters_DeltaZero_Missing_NoInitial(t *testing.T) {
	overrideCounter = func(bucket, scope, collection, id string, delta int64, initial *uint64, ttlSeconds *uint32) (uint64, uint64, error) {
		return 0, 0, assertError("not found")
	}
	t.Cleanup(func() { overrideCounter = nil })

	r := setupCountersRouter()
	r.Post("/buckets/{bucket}/counters/{id}", CounterOp(nil, nil))
	body := bytes.NewBufferString(`{"delta":0}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/metrics/counters/missing", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestCounters_InvalidPayload(t *testing.T) {
	r := setupCountersRouter()
	r.Post("/buckets/{bucket}/counters/{id}", CounterOp(nil, nil))
	body := bytes.NewBufferString(`{"delta":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/buckets/metrics/counters/c3", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
