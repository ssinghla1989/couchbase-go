package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// test override hooks
var (
	overrideGet        func(bucket, scope, collection, id string) (uint64, map[string]any, error)
	overrideInsert     func(bucket, scope, collection, id string, body json.RawMessage) (uint64, error)
	overrideUpsert     func(bucket, scope, collection, id string, body json.RawMessage) (uint64, error)
	overrideDelete     func(bucket, scope, collection, id string) (uint64, error)
	overrideReplaceCAS func(bucket, scope, collection, id string, cas uint64, body json.RawMessage) (uint64, error)
	overrideDeleteCAS  func(bucket, scope, collection, id string, cas uint64) (uint64, error)
)

func getScopeAndCollection(r *http.Request) (string, string) {
	scope := r.URL.Query().Get("scope")
	collection := r.URL.Query().Get("collection")
	return scope, collection
}

func mapErrorToStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, gocb.ErrDocumentNotFound) {
		return http.StatusNotFound
	}
	// Heuristic: treat plain-text not founds as 404
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	// Treat timeouts or obvious connectivity issues as 503
	if errors.Is(err, gocb.ErrTimeout) || strings.Contains(msg, "timeout") || strings.Contains(msg, "network") || strings.Contains(msg, "unavailable") {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func collectionFor(cb *couchbase.Client, bucketName, scopeName, collectionName string) (*gocb.Collection, error) {
	bucket := cb.Bucket(bucketName)
	if bucket == nil {
		return nil, fmt.Errorf("bucket not found")
	}
	_ = bucket.WaitUntilReady(2*time.Second, nil)
	if scopeName == "" || collectionName == "" {
		return bucket.DefaultCollection(), nil
	}
	return bucket.Scope(scopeName).Collection(collectionName), nil
}

// GetDocument handles fetching a document by ID.
// @Summary Get document
// @Description Fetch a document by ID from a Couchbase bucket
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Success 200 {object} map[string]any
// @Header 200 {string} ETag "CAS ETag of the document (quoted)"
// @Failure 404 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id} [get]
func GetDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")
		scope, collection := getScopeAndCollection(r)

		if overrideGet != nil {
			cas, content, err := overrideGet(bucket, scope, collection, id)
			if err != nil {
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusOK, map[string]any{
				"id":      id,
				"cas":     fmt.Sprintf("%d", cas),
				"content": content,
			})
			return
		}

		coll, err := collectionFor(cb, bucket, scope, collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}
		res, err := coll.Get(id, &gocb.GetOptions{Context: r.Context()})
		if err != nil {
			status := mapErrorToStatus(err)
			response.Error(w, status, err)
			return
		}
		var content map[string]any
		if err := res.Content(&content); err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		setETag(w, res.Cas())
		response.JSON(w, http.StatusOK, map[string]any{
			"id":      id,
			"cas":     fmt.Sprintf("%d", uint64(res.Cas())),
			"content": content,
		})
	}
}

// CreateDocument handles inserting a new document with a server-generated ID.
// @Summary Create document
// @Description Create a new document with server-generated UUID
// @Param bucket path string true "Bucket name"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Param prefix query string false "ID prefix"
// @Accept json
// @Produce json
// @Success 201 {object} map[string]string
// @Header 201 {string} ETag "CAS ETag of the created document (quoted)"
// @Failure 400 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs [post]
func CreateDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		scope, collection := getScopeAndCollection(r)
		prefix := r.URL.Query().Get("prefix")

		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}

		id := uuid.NewString()
		if prefix != "" {
			id = prefix + id
		}

		if overrideInsert != nil {
			cas, err := overrideInsert(bucket, scope, collection, id, body)
			if err != nil {
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
			return
		}

		coll, err := collectionFor(cb, bucket, scope, collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}
		res, err := coll.Insert(id, body, &gocb.InsertOptions{Context: r.Context()})
		if err != nil {
			status := mapErrorToStatus(err)
			response.Error(w, status, err)
			return
		}
		setETag(w, res.Cas())
		response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
	}
}

// UpsertDocument handles insert or replace of a document by ID.
// @Summary Upsert document (CAS-protected)
// @Description Upsert or replace a document. Conditional headers supported: If-None-Match:* for create-only; If-Match:"<cas>" for CAS-protected replace. Body fallbacks supported when headers not provided.
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Param If-None-Match header string false "Use * for create-only"
// @Param If-Match header string false "ETag with CAS value, quoted"
// @Param payload body object true "{"doc":object, "ttl_seconds":int, "if_match_cas":string, "if_none_match":bool}"
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Success 201 {object} map[string]string
// @Header 200 {string} ETag "CAS ETag of the updated document (quoted)"
// @Header 201 {string} ETag "CAS ETag of the created document (quoted)"
// @Failure 400 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 409 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id} [put]
func UpsertDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")
		scope, collection := getScopeAndCollection(r)

		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid request body"})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(rawBody))

		var bodyMap map[string]any
		_ = json.Unmarshal(rawBody, &bodyMap)

		// ttl_seconds (>=0)
		var ttlSeconds *int
		if v, ok := bodyMap["ttl_seconds"]; ok {
			switch t := v.(type) {
			case float64:
				iv := int(t)
				ttlSeconds = &iv
			case string:
				if t != "" {
					if iv, perr := strconv.Atoi(t); perr == nil {
						ttlSeconds = &iv
					} else {
						response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid ttl_seconds"})
						return
					}
				}
			}
			if ttlSeconds != nil && *ttlSeconds < 0 {
				response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "ttl_seconds must be >= 0"})
				return
			}
		}

		// doc payload
		var docRaw json.RawMessage
		if v, ok := bodyMap["doc"]; ok {
			b, _ := json.Marshal(v)
			docRaw = json.RawMessage(b)
		} else {
			docRaw = json.RawMessage(rawBody)
		}

		// conditions precedence
		var bodyIfNoneMatch *bool
		if v, ok := bodyMap["if_none_match"]; ok {
			switch t := v.(type) {
			case bool:
				bodyIfNoneMatch = &t
			case string:
				if t == "true" || t == "false" {
					bv := t == "true"
					bodyIfNoneMatch = &bv
				}
			}
		}
		var bodyIfMatchCASStr *string
		if v, ok := bodyMap["if_match_cas"]; ok {
			switch t := v.(type) {
			case float64:
				s := strconv.FormatUint(uint64(t), 10)
				bodyIfMatchCASStr = &s
			case string:
				if t != "" {
					bodyIfMatchCASStr = &t
				}
			}
		}

		createOnly := isCreateOnly(r, bodyIfNoneMatch)
		matchCASPtr, casErr := getMatchCAS(r, bodyIfMatchCASStr)
		if casErr != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": casErr.Error()})
			return
		}

		var expiryOpt time.Duration
		if ttlSeconds != nil {
			expiryOpt = time.Duration(*ttlSeconds) * time.Second
		}

		if createOnly {
			if overrideInsert != nil {
				cas, err := overrideInsert(bucket, scope, collection, id, docRaw)
				if err != nil {
					response.JSON(w, http.StatusConflict, map[string]any{"code": "conflict_exists", "message": err.Error()})
					return
				}
				setETag(w, gocb.Cas(cas))
				response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
				return
			}
			coll, err := collectionFor(cb, bucket, scope, collection)
			if err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
			res, err := coll.Insert(id, docRaw, &gocb.InsertOptions{Context: r.Context(), Expiry: expiryOpt})
			if err != nil {
				if errors.Is(err, gocb.ErrDocumentExists) {
					if logger != nil {
						logger.Warn("create-only conflict", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Bool("exists", true), zap.String("op", "insert"))
					}
					response.JSON(w, http.StatusConflict, map[string]any{"code": "conflict_exists", "message": "document already exists"})
					return
				}
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			setETag(w, res.Cas())
			response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
			return
		}

		if matchCASPtr != nil {
			matchCAS := *matchCASPtr
			if overrideReplaceCAS != nil {
				newCas, err := overrideReplaceCAS(bucket, scope, collection, id, uint64(matchCAS), docRaw)
				if err != nil {
					msg := strings.ToLower(err.Error())
					if strings.Contains(msg, "mismatch") {
						if logger != nil {
							logger.Warn("CAS mismatch on replace", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Uint64("expected_cas", uint64(matchCAS)), zap.String("op", "replace"))
						}
						response.JSON(w, http.StatusConflict, map[string]any{"code": "cas_mismatch", "message": "cas mismatch"})
						return
					}
					if strings.Contains(msg, "not found") {
						response.JSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "document not found"})
						return
					}
					status := mapErrorToStatus(err)
					response.Error(w, status, err)
					return
				}
				setETag(w, gocb.Cas(newCas))
				response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", newCas)})
				return
			}
			coll, err := collectionFor(cb, bucket, scope, collection)
			if err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
			res, err := coll.Replace(id, docRaw, &gocb.ReplaceOptions{Context: r.Context(), Cas: matchCAS, Expiry: expiryOpt})
			if err != nil {
				if errors.Is(err, gocb.ErrCasMismatch) {
					if logger != nil {
						logger.Warn("CAS mismatch on replace", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Uint64("expected_cas", uint64(matchCAS)), zap.String("op", "replace"))
					}
					response.JSON(w, http.StatusConflict, map[string]any{"code": "cas_mismatch", "message": "cas mismatch"})
					return
				}
				if errors.Is(err, gocb.ErrDocumentNotFound) {
					response.JSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "document not found"})
					return
				}
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			setETag(w, res.Cas())
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
			return
		}

		// Unconditional upsert
		if overrideUpsert != nil {
			cas, err := overrideUpsert(bucket, scope, collection, id, docRaw)
			if err != nil {
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
			return
		}
		coll, err := collectionFor(cb, bucket, scope, collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}
		res, err := coll.Upsert(id, docRaw, &gocb.UpsertOptions{Context: r.Context(), Expiry: expiryOpt})
		if err != nil {
			status := mapErrorToStatus(err)
			response.Error(w, status, err)
			return
		}
		setETag(w, res.Cas())
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
	}
}

// DeleteDocument handles deletion of a document by ID.
// @Summary Delete document (CAS-protected)
// @Description Delete a document. If `If-Match:"<cas>"` header (or body `if_match_cas`) is provided, the delete is CAS-protected. When `REQUIRE_CAS_ON_DELETE=true`, missing CAS returns 428.
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Param If-Match header string false "ETag with CAS value, quoted"
// @Param payload body object false "{"if_match_cas":string}"
// @Produce json
// @Success 200 {object} map[string]any
// @Header 200 {string} ETag "CAS ETag of the deleted document (quoted)"
// @Failure 400 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 409 {object} map[string]any
// @Failure 428 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id} [delete]
func DeleteDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")
		scope, collection := getScopeAndCollection(r)

		var bodyMap map[string]any
		if r.Body != nil {
			raw, _ := io.ReadAll(r.Body)
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &bodyMap)
			}
		}

		var bodyIfMatchCASStr *string
		if v, ok := bodyMap["if_match_cas"]; ok {
			switch t := v.(type) {
			case float64:
				s := strconv.FormatUint(uint64(t), 10)
				bodyIfMatchCASStr = &s
			case string:
				if t != "" {
					bodyIfMatchCASStr = &t
				}
			}
		}

		matchCASPtr, casErr := getMatchCAS(r, bodyIfMatchCASStr)
		if casErr != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": casErr.Error()})
			return
		}

		requireCAS := false
		if cb != nil && cb.Config() != nil {
			requireCAS = cb.Config().RequireCASOnDelete
		} else {
			requireCAS = strings.EqualFold(os.Getenv("REQUIRE_CAS_ON_DELETE"), "true")
		}
		if matchCASPtr == nil && requireCAS {
			response.JSON(w, http.StatusPreconditionRequired, map[string]any{"code": "precondition_required", "message": "CAS required for delete"})
			return
		}

		if matchCASPtr != nil {
			matchCAS := *matchCASPtr
			if overrideDeleteCAS != nil {
				oldCas, err := overrideDeleteCAS(bucket, scope, collection, id, uint64(matchCAS))
				if err != nil {
					msg := strings.ToLower(err.Error())
					if strings.Contains(msg, "mismatch") {
						if logger != nil {
							logger.Warn("CAS mismatch on delete", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Uint64("expected_cas", uint64(matchCAS)), zap.String("op", "delete"))
						}
						response.JSON(w, http.StatusConflict, map[string]any{"code": "cas_mismatch", "message": "cas mismatch"})
						return
					}
					if strings.Contains(msg, "not found") {
						response.JSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "document not found"})
						return
					}
					status := mapErrorToStatus(err)
					response.Error(w, status, err)
					return
				}
				setETag(w, gocb.Cas(oldCas))
				response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", oldCas)})
				return
			}
			coll, err := collectionFor(cb, bucket, scope, collection)
			if err != nil {
				response.Error(w, http.StatusServiceUnavailable, err)
				return
			}
			res, err := coll.Remove(id, &gocb.RemoveOptions{Context: r.Context(), Cas: matchCAS})
			if err != nil {
				if errors.Is(err, gocb.ErrCasMismatch) {
					if logger != nil {
						logger.Warn("CAS mismatch on delete", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Uint64("expected_cas", uint64(matchCAS)), zap.String("op", "delete"))
					}
					response.JSON(w, http.StatusConflict, map[string]any{"code": "cas_mismatch", "message": "cas mismatch"})
					return
				}
				if errors.Is(err, gocb.ErrDocumentNotFound) {
					response.JSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "document not found"})
					return
				}
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			setETag(w, res.Cas())
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
			return
		}

		// Unconditional delete
		if overrideDelete != nil {
			cas, err := overrideDelete(bucket, scope, collection, id)
			if err != nil {
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", cas)})
			return
		}

		coll, err := collectionFor(cb, bucket, scope, collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}
		res, err := coll.Remove(id, &gocb.RemoveOptions{Context: r.Context()})
		if err != nil {
			status := mapErrorToStatus(err)
			response.Error(w, status, err)
			return
		}
		setETag(w, res.Cas())
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
	}
}
