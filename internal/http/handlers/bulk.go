package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// limits are read from config via cb.Config(), but keep fallbacks here
const (
	defaultBulkMaxWorkers     = 8
	defaultBulkGetMaxIDs      = 1000
	defaultBulkUpsertMaxItems = 500
	defaultBulkTimeout        = 5 * time.Second
)

type bulkError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bulkGetRequest struct {
	IDs        []string `json:"ids"`
	Scope      string   `json:"scope"`
	Collection string   `json:"collection"`
}

type bulkGetItemResult struct {
	ID          string     `json:"id"`
	OK          bool       `json:"ok"`
	CAS         string     `json:"cas,omitempty"`
	Content     any        `json:"content,omitempty"`
	ContentType string     `json:"content_type,omitempty"`
	Error       *bulkError `json:"error,omitempty"`
}

type bulkGetResponse struct {
	Bucket     string              `json:"bucket"`
	Scope      string              `json:"scope"`
	Collection string              `json:"collection"`
	Results    []bulkGetItemResult `json:"results"`
}

type bulkUpsertItem struct {
	ID         string          `json:"id"`
	Doc        json.RawMessage `json:"doc"`
	TTLSeconds *uint32         `json:"ttl_seconds,omitempty"`
}

type bulkUpsertRequest struct {
	Items      []bulkUpsertItem `json:"items"`
	Scope      string           `json:"scope"`
	Collection string           `json:"collection"`
	Durability string           `json:"durability"`
}

type bulkUpsertItemResult struct {
	ID    string     `json:"id"`
	OK    bool       `json:"ok"`
	CAS   string     `json:"cas,omitempty"`
	Error *bulkError `json:"error,omitempty"`
}

type bulkUpsertResponse struct {
	Bucket     string                 `json:"bucket"`
	Scope      string                 `json:"scope"`
	Collection string                 `json:"collection"`
	Results    []bulkUpsertItemResult `json:"results"`
}

// test override hooks (used by unit tests to simulate behaviors)
var (
	overrideBulkGet    func(bucket, scope, collection string, ids []string) ([]bulkGetItemResult, error)
	overrideBulkUpsert func(bucket, scope, collection, durability string, items []bulkUpsertItem) ([]bulkUpsertItemResult, error)
	overrideBulkReady  func(timeout time.Duration) error
)

func durabilityLevelFromString(s string) (gocb.DurabilityLevel, bool) {
	switch s {
	case "", "none":
		return gocb.DurabilityLevelNone, true
	case "majority":
		return gocb.DurabilityLevelMajority, true
	case "persist_to_majority":
		return gocb.DurabilityLevelPersistToMajority, true
	default:
		return gocb.DurabilityLevelNone, false
	}
}

func getBulkConfig(cb *couchbase.Client) (maxWorkers, maxGet, maxUpsert int, timeout time.Duration) {
	cfg := cb.Config()
	maxWorkers, maxGet, maxUpsert = defaultBulkMaxWorkers, defaultBulkGetMaxIDs, defaultBulkUpsertMaxItems
	timeout = defaultBulkTimeout
	if cfg != nil {
		if cfg.BulkMaxWorkers > 0 {
			maxWorkers = cfg.BulkMaxWorkers
		}
		if cfg.BulkGetMaxIDs > 0 {
			maxGet = cfg.BulkGetMaxIDs
		}
		if cfg.BulkUpsertMaxItems > 0 {
			maxUpsert = cfg.BulkUpsertMaxItems
		}
		if cfg.BulkHandlerTimeout > 0 {
			timeout = cfg.BulkHandlerTimeout
		}
	}
	return
}

func mapItemError(err error) *bulkError {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if errors.Is(err, gocb.ErrDocumentNotFound) || containsFold(msg, "not found") {
		return &bulkError{Code: "not_found", Message: "document not found"}
	}
	if errors.Is(err, gocb.ErrTimeout) || containsFold(msg, "timeout") {
		return &bulkError{Code: "timeout", Message: msg}
	}
	if containsFold(msg, "unavailable") || containsFold(msg, "network") {
		return &bulkError{Code: "unavailable", Message: msg}
	}
	return &bulkError{Code: "internal", Message: msg}
}

func containsFold(haystack, needle string) bool {
	return len(haystack) > 0 && len(needle) > 0 &&
		strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// BulkGet fetches multiple documents concurrently.
// @Summary Bulk get documents
// @Description Best-effort fetch multiple documents; per-item result included. 200 even with partial failures.
// @Accept json
// @Produce json
// @Param bucket path string true "Bucket name"
// @Param scope query string false "Scope name (default _default)"
// @Param collection query string false "Collection name (default _default)"
// @Param request body bulkGetRequest true "Bulk get request"
// @Success 200 {object} bulkGetResponse
// @Failure 400 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/_bulk_get [post]
func BulkGet(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucketName := chi.URLParam(r, "bucket")
		var req bulkGetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}
		// allow query param defaults
		if req.Scope == "" {
			req.Scope = r.URL.Query().Get("scope")
		}
		if req.Collection == "" {
			req.Collection = r.URL.Query().Get("collection")
		}
		if req.Scope == "" {
			req.Scope = "_default"
		}
		if req.Collection == "" {
			req.Collection = "_default"
		}

		maxWorkers, maxGet, _, timeout := getBulkConfig(cb)
		if len(req.IDs) == 0 {
			response.Error(w, http.StatusBadRequest, errors.New("ids is required"))
			return
		}
		if len(req.IDs) > maxGet {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("ids length exceeds limit %d", maxGet))
			return
		}

		// override path short-circuit (before any Couchbase usage)
		if overrideBulkGet != nil {
			results, err := overrideBulkGet(bucketName, req.Scope, req.Collection, req.IDs)
			if err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
			response.JSON(w, http.StatusOK, bulkGetResponse{Bucket: bucketName, Scope: req.Scope, Collection: req.Collection, Results: results})
			return
		}

		// readiness check
		if overrideBulkReady != nil {
			if err := overrideBulkReady(timeout); err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
		} else if cb != nil && cb.Cluster() != nil {
			if err := cb.Cluster().WaitUntilReady(timeout, &gocb.WaitUntilReadyOptions{Context: r.Context()}); err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
		}

		coll, err := collectionFor(cb, bucketName, req.Scope, req.Collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}



		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		results := make([]bulkGetItemResult, len(req.IDs))
		successCount := 0
		failCount := 0

		// worker pool
		workerCount := len(req.IDs)
		if workerCount > maxWorkers {
			workerCount = maxWorkers
		}
		jobs := make(chan int, len(req.IDs))
		var wg sync.WaitGroup
		wg.Add(workerCount)
		for widx := 0; widx < workerCount; widx++ {
			go func() {
				defer wg.Done()
				for idx := range jobs {
					id := req.IDs[idx]
					res := bulkGetItemResult{ID: id}
					getRes, err := coll.Get(id, &gocb.GetOptions{Context: ctx})
					if err != nil {
						res.OK = false
						res.Error = mapItemError(err)
						results[idx] = res
						continue
					}
					// Try JSON first
					var obj any
					if err := getRes.Content(&obj); err == nil {
						res.OK = true
						res.CAS = fmt.Sprintf("%d", uint64(getRes.Cas()))
						res.Content = obj
						results[idx] = res
						continue
					}
					// Fallback: re-fetch raw bytes with binary transcoder
					rawRes, rerr := coll.Get(id, &gocb.GetOptions{Context: ctx, Transcoder: gocb.NewRawBinaryTranscoder()})
					if rerr != nil {
						res.OK = false
						res.Error = mapItemError(rerr)
						results[idx] = res
						continue
					}
					var b []byte
					if cerr := rawRes.Content(&b); cerr != nil {
						res.OK = false
						res.Error = &bulkError{Code: "internal", Message: cerr.Error()}
						results[idx] = res
						continue
					}
					res.OK = true
					res.CAS = fmt.Sprintf("%d", uint64(rawRes.Cas()))
					res.ContentType = "application/octet-stream"
					res.Content = base64.StdEncoding.EncodeToString(b)
					results[idx] = res
				}
			}()
		}
		for i := range req.IDs {
			jobs <- i
		}
		close(jobs)
		wg.Wait()

		for _, r := range results {
			if r.OK {
				successCount++
			} else {
				failCount++
			}
		}
		if logger != nil {
			logger.Info("bulk_get completed", zap.Int("success", successCount), zap.Int("fail", failCount), zap.Int("total", len(results)))
		}
		response.JSON(w, http.StatusOK, bulkGetResponse{
			Bucket:     bucketName,
			Scope:      req.Scope,
			Collection: req.Collection,
			Results:    results,
		})
	}
}

// BulkUpsert performs upsert for multiple documents concurrently.
// @Summary Bulk upsert documents
// @Description Best-effort upsert multiple documents; per-item result included. 200 even with partial failures.
// @Accept json
// @Produce json
// @Param bucket path string true "Bucket name"
// @Param scope query string false "Scope name (default _default)"
// @Param collection query string false "Collection name (default _default)"
// @Param request body bulkUpsertRequest true "Bulk upsert request"
// @Success 200 {object} bulkUpsertResponse
// @Failure 400 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/_bulk_upsert [post]
func BulkUpsert(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucketName := chi.URLParam(r, "bucket")
		var req bulkUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}
		if req.Scope == "" {
			req.Scope = r.URL.Query().Get("scope")
		}
		if req.Collection == "" {
			req.Collection = r.URL.Query().Get("collection")
		}
		if req.Scope == "" {
			req.Scope = "_default"
		}
		if req.Collection == "" {
			req.Collection = "_default"
		}

		maxWorkers, _, maxUpsert, timeout := getBulkConfig(cb)
		if len(req.Items) == 0 {
			response.Error(w, http.StatusBadRequest, errors.New("items is required"))
			return
		}
		if len(req.Items) > maxUpsert {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("items length exceeds limit %d", maxUpsert))
			return
		}

		// pre-parse items and mark invalid per-item (do not 400)
		parsedDocs := make([]map[string]any, len(req.Items))
		invalidIdx := make(map[int]*bulkError)
		for i, it := range req.Items {
			if strings.TrimSpace(it.ID) == "" {
				invalidIdx[i] = &bulkError{Code: "invalid", Message: "id is required"}
				continue
			}
			var tmp any
			if err := json.Unmarshal(it.Doc, &tmp); err != nil {
				invalidIdx[i] = &bulkError{Code: "invalid", Message: "doc must be valid JSON"}
				continue
			}
			obj, ok := tmp.(map[string]any)
			if !ok {
				invalidIdx[i] = &bulkError{Code: "invalid", Message: "doc must be an object"}
				continue
			}
			parsedDocs[i] = obj
		}

		durLevel, ok := durabilityLevelFromString(req.Durability)
		if !ok {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("invalid durability: %s", req.Durability))
			return
		}

		// override path short-circuit (before any Couchbase usage)
		if overrideBulkUpsert != nil {
			results, err := overrideBulkUpsert(bucketName, req.Scope, req.Collection, req.Durability, req.Items)
			if err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
			response.JSON(w, http.StatusOK, bulkUpsertResponse{Bucket: bucketName, Scope: req.Scope, Collection: req.Collection, Results: results})
			return
		}

		// readiness check
		if overrideBulkReady != nil {
			if err := overrideBulkReady(timeout); err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
		} else if cb != nil && cb.Cluster() != nil {
			if err := cb.Cluster().WaitUntilReady(timeout, &gocb.WaitUntilReadyOptions{Context: r.Context()}); err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
		}

		coll, err := collectionFor(cb, bucketName, req.Scope, req.Collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}



		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		results := make([]bulkUpsertItemResult, len(req.Items))
		successCount := 0
		failCount := 0

		workerCount := len(req.Items)
		if workerCount > maxWorkers {
			workerCount = maxWorkers
		}
		jobs := make(chan int, len(req.Items))
		var wg sync.WaitGroup
		wg.Add(workerCount)
		for widx := 0; widx < workerCount; widx++ {
			go func() {
				defer wg.Done()
				for idx := range jobs {
					item := req.Items[idx]
					res := bulkUpsertItemResult{ID: item.ID}
					if errv, exists := invalidIdx[idx]; exists {
						res.OK = false
						res.Error = errv
						results[idx] = res
						continue
					}
					opts := &gocb.UpsertOptions{Context: ctx}
					if item.TTLSeconds != nil {
						opts.Expiry = time.Duration(*item.TTLSeconds) * time.Second
					}
					if durLevel != gocb.DurabilityLevelNone {
						opts.DurabilityLevel = durLevel
					}
					upRes, err := coll.Upsert(item.ID, parsedDocs[idx], opts)
					if err != nil {
						res.OK = false
						res.Error = mapItemError(err)
						results[idx] = res
						continue
					}
					res.OK = true
					res.CAS = fmt.Sprintf("%d", uint64(upRes.Cas()))
					results[idx] = res
				}
			}()
		}
		for i := range req.Items {
			jobs <- i
		}
		close(jobs)
		wg.Wait()

		for _, r := range results {
			if r.OK {
				successCount++
			} else {
				failCount++
			}
		}
		if logger != nil {
			logger.Info("bulk_upsert completed", zap.Int("success", successCount), zap.Int("fail", failCount), zap.Int("total", len(results)))
		}
		response.JSON(w, http.StatusOK, bulkUpsertResponse{
			Bucket:     bucketName,
			Scope:      req.Scope,
			Collection: req.Collection,
			Results:    results,
		})
	}
}
