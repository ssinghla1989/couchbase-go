package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// test override hooks for meta/existence
var (
	overrideExists  func(bucket, scope, collection, id string) (exists bool, cas uint64, err error)
	overrideGetMeta func(bucket, scope, collection, id string) (cas uint64, expiry *time.Time, found bool, err error)
)

// HeadDocument performs a lightweight existence check without returning the body.
// @Summary Document existence check
// @Description HEAD existence check; returns 200 with ETag when exists; 404 when missing
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Success 200 "OK (no body)"
// @Header 200 {string} ETag "CAS ETag of the document (quoted)"
// @Failure 404 "Not Found (no body)"
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id} [head]
func HeadDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")
		scope, collection := getScopeAndCollection(r)

		if overrideExists != nil {
			exists, cas, err := overrideExists(bucket, scope, collection, id)
			if err != nil {
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("exists override failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("exists override failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			setETag(w, gocb.Cas(cas))
			w.WriteHeader(http.StatusOK)
			return
		}

		coll, code, err := resolveCollectionHTTP(cb, bucket, scope, collection, r.Context())
		if err != nil {
			if code >= 500 {
				if logger != nil {
					logger.Error("collection resolution failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("collection resolution failed", zap.Error(err))
				}
			}
			response.JSON(w, code, map[string]any{"code": "unavailable", "message": err.Error()})
			return
		}

		res, err := coll.Exists(id, &gocb.ExistsOptions{Context: r.Context()})
		if err != nil {
			status := mapErrorToStatus(err)
			if status >= 500 {
				if logger != nil {
					logger.Error("exists failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("exists failed", zap.Error(err))
				}
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
			return
		}
		if !res.Exists() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		setETag(w, res.Cas())
		w.WriteHeader(http.StatusOK)
	}
}

// GetDocumentMeta returns lightweight metadata (id, cas, expiry) for a document.
// @Summary Get document metadata
// @Description Fetch lightweight metadata (CAS and expiry) for a document
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Produce json
// @Success 200 {object} map[string]any
// @Header 200 {string} ETag "CAS ETag of the document (quoted)"
// @Failure 404 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id}/meta [get]
func GetDocumentMeta(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")
		scope, collection := getScopeAndCollection(r)

		if overrideGetMeta != nil {
			cas, expiryPtr, found, err := overrideGetMeta(bucket, scope, collection, id)
			if err != nil {
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("meta override failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("meta override failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			if !found {
				response.JSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "document not found"}})
				return
			}
			setETag(w, gocb.Cas(cas))
			var expiryVal any
			if expiryPtr != nil && !expiryPtr.IsZero() {
				expiryVal = expiryPtr.UTC().Format(time.RFC3339)
			} else {
				expiryVal = nil
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas), "expiry": expiryVal})
			return
		}

		coll, code, err := resolveCollectionHTTP(cb, bucket, scope, collection, r.Context())
		if err != nil {
			if code >= 500 {
				if logger != nil {
					logger.Error("collection resolution failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("collection resolution failed", zap.Error(err))
				}
			}
			response.JSON(w, code, map[string]any{"code": "unavailable", "message": err.Error()})
			return
		}

		// Preferred simple approach: Get with WithExpiry and ignore content
		getRes, err := coll.Get(id, &gocb.GetOptions{Context: r.Context(), WithExpiry: true})
		if err != nil {
			status := mapErrorToStatus(err)
			if status >= 500 {
				if logger != nil {
					logger.Error("get meta failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("get meta failed", zap.Error(err))
				}
			}
			if status == http.StatusNotFound {
				response.JSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": err.Error()}})
				return
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
			return
		}
		setETag(w, getRes.Cas())
		var expiryVal any
		if !getRes.ExpiryTime().IsZero() {
			expiryVal = getRes.ExpiryTime().UTC().Format(time.RFC3339)
		} else {
			expiryVal = nil
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":     id,
			"cas":    fmt.Sprintf("%d", uint64(getRes.Cas())),
			"expiry": expiryVal,
		})
	}
}
