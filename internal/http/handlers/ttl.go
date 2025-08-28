package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// test override hook for touch
var (
	overrideTouch func(bucket, scope, collection, id string, ttlSeconds int64) (cas uint64, err error)
)

type touchRequest struct {
	TTLSeconds any    `json:"ttl_seconds"`
	Scope      string `json:"scope"`
	Collection string `json:"collection"`
}

func parsePositiveTTLSeconds(v any) (int64, error) {
	switch t := v.(type) {
	case float64:
		if t != math.Trunc(t) {
			return 0, fmt.Errorf("invalid ttl_seconds")
		}
		iv := int64(t)
		if iv < 1 {
			return 0, fmt.Errorf("ttl_seconds must be >= 1")
		}
		if iv > 2147483647 {
			return 0, fmt.Errorf("invalid ttl_seconds")
		}
		return iv, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, fmt.Errorf("invalid ttl_seconds")
		}
		iv, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid ttl_seconds")
		}
		if iv < 1 {
			return 0, fmt.Errorf("ttl_seconds must be >= 1")
		}
		if iv > 2147483647 {
			return 0, fmt.Errorf("invalid ttl_seconds")
		}
		return iv, nil
	default:
		return 0, fmt.Errorf("invalid ttl_seconds")
	}
}

// TouchDocument refreshes the expiry (TTL) of a document.
// @Summary Refresh document expiry (touch)
// @Description Refresh TTL of a document. ttl_seconds must be >= 1. Use PUT with ttl_seconds:0 to clear expiry.
// @Accept json
// @Produce json
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name (default _default)"
// @Param collection query string false "Collection name (default _default)"
// @Param request body touchRequest true "{"ttl_seconds":int, "scope":string, "collection":string}"
// @Success 200 {object} map[string]any
// @Header 200 {string} ETag "CAS ETag of the document (quoted)"
// @Failure 400 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id}/touch [post]
func TouchDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")

		var req touchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid JSON body"})
			return
		}
		// merge scope/collection from body with query params and defaults
		scope, collection := resolveScopeAndCollection(req.Scope, req.Collection, r)

		if req.TTLSeconds == nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "ttl_seconds is required"})
			return
		}
		ttlSeconds, perr := parsePositiveTTLSeconds(req.TTLSeconds)
		if perr != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": perr.Error()})
			return
		}

		// test override
		if overrideTouch != nil {
			cas, err := overrideTouch(bucket, scope, collection, id, ttlSeconds)
			if err != nil {
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Debug("touch override failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
			if logger != nil {
				logger.Info("touch",
					zap.String("op", "touch"),
					zap.String("bucket", bucket),
					zap.String("scope", scope),
					zap.String("collection", collection),
					zap.String("id", id),
					zap.Int64("ttl_seconds", ttlSeconds),
					zap.Duration("latency", time.Since(start)),
				)
			}
			return
		}

		coll, code, err := resolveCollectionHTTP(cb, bucket, scope, collection, r.Context())
		if err != nil {
			if logger != nil {
				logger.Warn("collection resolution failed", zap.Error(err))
			}
			response.JSON(w, code, map[string]any{"code": "unavailable", "message": err.Error()})
			return
		}

		exp := time.Duration(ttlSeconds) * time.Second
		res, err := coll.Touch(id, exp, &gocb.TouchOptions{Context: r.Context()})
		if err != nil {
			status := mapErrorToStatus(err)
			if status == http.StatusNotFound {
				response.JSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "document not found"}})
				return
			}
			if status >= http.StatusInternalServerError && logger != nil {
				logger.Debug("touch failed", zap.Error(err))
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
			return
		}
		setETag(w, res.Cas())
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
		if logger != nil {
			logger.Info("touch",
				zap.String("op", "touch"),
				zap.String("bucket", bucket),
				zap.String("scope", scope),
				zap.String("collection", collection),
				zap.String("id", id),
				zap.Int64("ttl_seconds", ttlSeconds),
				zap.Duration("latency", time.Since(start)),
			)
		}
	}
}
