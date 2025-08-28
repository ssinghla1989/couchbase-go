package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
	// TTL-aware overrides used by tests to observe provided expiry
	overrideInsertWithExpiry     func(bucket, scope, collection, id string, body json.RawMessage, expirySeconds int64) (uint64, error)
	overrideUpsertWithExpiry     func(bucket, scope, collection, id string, body json.RawMessage, expirySeconds int64) (uint64, error)
	overrideReplaceCASWithExpiry func(bucket, scope, collection, id string, cas uint64, body json.RawMessage, expirySeconds int64) (uint64, error)
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
	// substring-based mapping to support overrides and generic errors
	msg := strings.ToLower(err.Error())
	if errors.Is(err, gocb.ErrDocumentNotFound) || strings.Contains(msg, "not found") {
		return http.StatusNotFound
	}
	if errors.Is(err, gocb.ErrDocumentExists) || strings.Contains(msg, "exists") || strings.Contains(msg, "conflict") {
		return http.StatusConflict
	}
	if errors.Is(err, gocb.ErrCasMismatch) || strings.Contains(msg, "cas mismatch") || strings.Contains(msg, "mismatch") {
		return http.StatusConflict
	}
	if errors.Is(err, gocb.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || strings.Contains(msg, "timeout") || strings.Contains(msg, "unavailable") || strings.Contains(msg, "network") {
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

// resolveCollectionHTTP wraps couchbase.ResolveCollection and maps errors to HTTP status.
func resolveCollectionHTTP(cb *couchbase.Client, bucketName, scopeName, collectionName string, ctx context.Context) (*gocb.Collection, int, error) {
	coll, err := couchbase.ResolveCollection(cb, bucketName, scopeName, collectionName, ctx)
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("bucket not ready")
	}
	return coll, http.StatusOK, nil
}

// writeJSON ensures consistent content-type and body schema.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	if w == nil {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.JSON(w, status, payload)
}

// okWithETag writes an {id, cas} payload with ETag header set to the quoted CAS.
func okWithETag(w http.ResponseWriter, status int, id string, cas gocb.Cas) {
	setETag(w, cas)
	writeJSON(w, status, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(cas))})
}

// upsertInput holds validated inputs for the upsert flow.
type upsertInput struct {
	Doc        json.RawMessage
	Expiry     *time.Duration
	CreateOnly bool
	MatchCAS   *gocb.Cas
}

// parseUpsertBody strictly validates body and headers, returning an HTTP status for validation errors.
func parseUpsertBody(r *http.Request, logger *zap.Logger) (upsertInput, int, error) {
	var in upsertInput

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "request body too large") {
			return in, http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large")
		}
		return in, http.StatusBadRequest, fmt.Errorf("invalid request body")
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))

	var body map[string]any
	if len(bytes.TrimSpace(raw)) == 0 {
		return in, http.StatusBadRequest, fmt.Errorf("invalid JSON body")
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return in, http.StatusBadRequest, fmt.Errorf("invalid JSON body")
	}

	// TTL parsing (integer only, >= 0)
	var ttlProvided bool
	var ttlSeconds int64
	if v, ok := body["ttl_seconds"]; ok {
		ttlProvided = true
		switch t := v.(type) {
		case float64:
			if t != math.Trunc(t) {
				return in, http.StatusBadRequest, fmt.Errorf("invalid ttl_seconds")
			}
			ttlSeconds = int64(t)
		case string:
			if strings.TrimSpace(t) == "" {
				return in, http.StatusBadRequest, fmt.Errorf("invalid ttl_seconds")
			}
			iv, perr := strconv.ParseInt(t, 10, 64)
			if perr != nil {
				return in, http.StatusBadRequest, fmt.Errorf("invalid ttl_seconds")
			}
			ttlSeconds = iv
		default:
			return in, http.StatusBadRequest, fmt.Errorf("invalid ttl_seconds")
		}
		if ttlSeconds < 0 {
			return in, http.StatusBadRequest, fmt.Errorf("ttl_seconds must be >= 0")
		}
		// enforce upper bound (approx int32 max seconds)
		if ttlSeconds > 2147483647 {
			return in, http.StatusBadRequest, fmt.Errorf("invalid ttl_seconds")
		}
		d := time.Duration(ttlSeconds) * time.Second
		in.Expiry = &d
	}

	// If-None-Match: only * allowed (quoted or not). Body fallback: if_none_match (bool)
	var headerINMSet bool
	var headerINMStar bool
	if raw := strings.TrimSpace(r.Header.Get("If-None-Match")); raw != "" {
		headerINMSet = true
		if raw == "*" || (len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && raw[1:len(raw)-1] == "*") {
			headerINMStar = true
		} else {
			return in, http.StatusBadRequest, fmt.Errorf("invalid If-None-Match; only * is allowed")
		}
	}
	var bodyINM *bool
	if v, ok := body["if_none_match"]; ok {
		b, ok := v.(bool)
		if !ok {
			return in, http.StatusBadRequest, fmt.Errorf("invalid if_none_match")
		}
		bodyINM = &b
	}
	if headerINMSet {
		if bodyINM != nil && *bodyINM != headerINMStar {
			return in, http.StatusBadRequest, fmt.Errorf("invalid If-None-Match; only * is allowed")
		}
		in.CreateOnly = headerINMStar
	} else if bodyINM != nil {
		in.CreateOnly = *bodyINM
	}

	// If-Match header: quoted decimal only, no weak validators. Body if_match_cas must be string.
	var headerCASSet bool
	var headerCAS string
	if rawIfMatch := strings.TrimSpace(r.Header.Get("If-Match")); rawIfMatch != "" {
		if strings.HasPrefix(rawIfMatch, "W/") {
			return in, http.StatusBadRequest, fmt.Errorf("invalid If-Match header")
		}
		if !(len(rawIfMatch) >= 2 && rawIfMatch[0] == '"' && rawIfMatch[len(rawIfMatch)-1] == '"') {
			return in, http.StatusBadRequest, fmt.Errorf("invalid If-Match header")
		}
		headerCAS = rawIfMatch[1 : len(rawIfMatch)-1]
		headerCASSet = true
	}
	var bodyCASPtr *string
	if v, ok := body["if_match_cas"]; ok {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return in, http.StatusBadRequest, fmt.Errorf("if_match_cas must be a quoted string")
		}
		s = strings.TrimSpace(s)
		bodyCASPtr = &s
	}
	switch {
	case headerCASSet && bodyCASPtr != nil && headerCAS != *bodyCASPtr:
		return in, http.StatusBadRequest, fmt.Errorf("conflicting CAS in header and body")
	case headerCASSet:
		cas, perr := parseCASString(headerCAS)
		if perr != nil {
			return in, http.StatusBadRequest, fmt.Errorf("invalid If-Match header")
		}
		in.MatchCAS = &cas
	case !headerCASSet && bodyCASPtr != nil:
		cas, perr := parseCASString(*bodyCASPtr)
		if perr != nil {
			return in, http.StatusBadRequest, fmt.Errorf("invalid if_match_cas")
		}
		in.MatchCAS = &cas
	}

	// Document extraction rule
	controlPresent := ttlProvided || headerINMSet || bodyINM != nil || headerCASSet || bodyCASPtr != nil
	if v, ok := body["doc"]; ok {
		b, _ := json.Marshal(v)
		in.Doc = json.RawMessage(b)
	} else if controlPresent {
		return in, http.StatusBadRequest, fmt.Errorf("when using ttl/conditions, provide a 'doc' field")
	} else {
		in.Doc = json.RawMessage(raw)
	}

	return in, http.StatusOK, nil
}

// Operation helpers
func doCreateOnly(coll *gocb.Collection, id string, body json.RawMessage, expiry *time.Duration, ctx context.Context) (gocb.Cas, error) {
	var exp time.Duration
	if expiry != nil {
		exp = *expiry
	}
	res, err := coll.Insert(id, body, &gocb.InsertOptions{Context: ctx, Expiry: exp})
	if err != nil {
		return gocb.Cas(0), err
	}
	return res.Cas(), nil
}

func doReplaceCAS(coll *gocb.Collection, id string, body json.RawMessage, cas gocb.Cas, expiry *time.Duration, ctx context.Context) (gocb.Cas, error) {
	var exp time.Duration
	if expiry != nil {
		exp = *expiry
	}
	res, err := coll.Replace(id, body, &gocb.ReplaceOptions{Context: ctx, Cas: cas, Expiry: exp})
	if err != nil {
		return gocb.Cas(0), err
	}
	return res.Cas(), nil
}

func doUnconditionalUpsert(coll *gocb.Collection, id string, body json.RawMessage, expiry *time.Duration, ctx context.Context) (gocb.Cas, error) {
	var exp time.Duration
	if expiry != nil {
		exp = *expiry
	}
	res, err := coll.Upsert(id, body, &gocb.UpsertOptions{Context: ctx, Expiry: exp})
	if err != nil {
		return gocb.Cas(0), err
	}
	return res.Cas(), nil
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
		res, err := coll.Get(id, &gocb.GetOptions{Context: r.Context()})
		if err != nil {
			status := mapErrorToStatus(err)
			if status >= 500 {
				if logger != nil {
					logger.Error("get failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("get failed", zap.Error(err))
				}
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
			return
		}
		var content map[string]any
		if err := res.Content(&content); err != nil {
			if logger != nil {
				logger.Error("decode content failed", zap.Error(err))
			}
			response.JSON(w, http.StatusInternalServerError, map[string]any{"code": "decode_error", "message": err.Error()})
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

		// limit body and parse with shared validator to allow ttl_seconds
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		in, code, err := parseUpsertBody(r, logger)
		if err != nil {
			if code >= 500 {
				if logger != nil {
					logger.Error("create validation failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("create validation failed", zap.Error(err))
				}
			}
			response.JSON(w, code, map[string]any{"code": "invalid", "message": err.Error()})
			return
		}
		// Do not allow conditional semantics on POST
		if in.CreateOnly || in.MatchCAS != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "conditional fields are not supported on create"})
			return
		}

		id := uuid.NewString()
		if prefix != "" {
			id = prefix + id
		}

		if overrideInsertWithExpiry != nil || overrideInsert != nil {
			if overrideInsertWithExpiry != nil {
				var ttlSeconds int64
				if in.Expiry != nil {
					ttlSeconds = int64((*in.Expiry) / time.Second)
				}
				cas, err := overrideInsertWithExpiry(bucket, scope, collection, id, in.Doc, ttlSeconds)
				if err != nil {
					status := mapErrorToStatus(err)
					if status >= 500 {
						if logger != nil {
							logger.Error("override insert failed", zap.Error(err))
						}
					} else {
						if logger != nil {
							logger.Warn("override insert failed", zap.Error(err))
						}
					}
					response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
					return
				}
				setETag(w, gocb.Cas(cas))
				response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
				return
			}
			cas, err := overrideInsert(bucket, scope, collection, id, in.Doc)
			if err != nil {
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("override insert failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("override insert failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
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
		var exp time.Duration
		if in.Expiry != nil {
			exp = *in.Expiry
		}
		res, err := coll.Insert(id, in.Doc, &gocb.InsertOptions{Context: r.Context(), Expiry: exp})
		if err != nil {
			status := mapErrorToStatus(err)
			if status >= 500 {
				if logger != nil {
					logger.Error("insert failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("insert failed", zap.Error(err))
				}
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
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

		// limit request body to 10MB
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

		in, code, err := parseUpsertBody(r, logger)
		if err != nil {
			if code >= 500 {
				if logger != nil {
					logger.Error("upsert validation failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("upsert validation failed", zap.Error(err))
				}
			}
			response.JSON(w, code, map[string]any{"code": "invalid", "message": err.Error()})
			return
		}

		// Overrides first
		if in.CreateOnly && (overrideInsertWithExpiry != nil || overrideInsert != nil) {
			if overrideInsertWithExpiry != nil {
				var ttlSeconds int64
				if in.Expiry != nil {
					ttlSeconds = int64((*in.Expiry) / time.Second)
				}
				cas, err := overrideInsertWithExpiry(bucket, scope, collection, id, in.Doc, ttlSeconds)
				if err != nil {
					if errors.Is(err, gocb.ErrDocumentExists) {
						if logger != nil {
							logger.Warn("create-only conflict", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.String("op", "insert"))
						}
						response.JSON(w, http.StatusConflict, map[string]any{"code": "conflict_exists", "message": "document already exists"})
						return
					}
					status := mapErrorToStatus(err)
					if status >= 500 {
						if logger != nil {
							logger.Error("override insert failed", zap.Error(err))
						}
					} else {
						if logger != nil {
							logger.Warn("override insert failed", zap.Error(err))
						}
					}
					response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
					return
				}
				setETag(w, gocb.Cas(cas))
				response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
				return
			}
			cas, err := overrideInsert(bucket, scope, collection, id, in.Doc)
			if err != nil {
				if errors.Is(err, gocb.ErrDocumentExists) {
					if logger != nil {
						logger.Warn("create-only conflict", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.String("op", "insert"))
					}
					response.JSON(w, http.StatusConflict, map[string]any{"code": "conflict_exists", "message": "document already exists"})
					return
				}
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("override insert failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("override insert failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
			return
		}

		if in.MatchCAS != nil && (overrideReplaceCASWithExpiry != nil || overrideReplaceCAS != nil) {
			if overrideReplaceCASWithExpiry != nil {
				var ttlSeconds int64
				if in.Expiry != nil {
					ttlSeconds = int64((*in.Expiry) / time.Second)
				}
				newCas, err := overrideReplaceCASWithExpiry(bucket, scope, collection, id, uint64(*in.MatchCAS), in.Doc, ttlSeconds)
				if err != nil {
					if errors.Is(err, gocb.ErrCasMismatch) {
						if logger != nil {
							logger.Warn("CAS mismatch on replace", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Uint64("expected_cas", uint64(*in.MatchCAS)), zap.String("op", "replace"))
						}
						response.JSON(w, http.StatusConflict, map[string]any{"code": "cas_mismatch", "message": "cas mismatch"})
						return
					}
					if errors.Is(err, gocb.ErrDocumentNotFound) {
						response.JSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "document not found"})
						return
					}
					status := mapErrorToStatus(err)
					if status >= 500 {
						if logger != nil {
							logger.Error("override replace failed", zap.Error(err))
						}
					} else {
						if logger != nil {
							logger.Warn("override replace failed", zap.Error(err))
						}
					}
					response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
					return
				}
				setETag(w, gocb.Cas(newCas))
				response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", newCas)})
				return
			}
			newCas, err := overrideReplaceCAS(bucket, scope, collection, id, uint64(*in.MatchCAS), in.Doc)
			if err != nil {
				if errors.Is(err, gocb.ErrCasMismatch) {
					if logger != nil {
						logger.Warn("CAS mismatch on replace", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Uint64("expected_cas", uint64(*in.MatchCAS)), zap.String("op", "replace"))
					}
					response.JSON(w, http.StatusConflict, map[string]any{"code": "cas_mismatch", "message": "cas mismatch"})
					return
				}
				if errors.Is(err, gocb.ErrDocumentNotFound) {
					response.JSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "document not found"})
					return
				}
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("override replace failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("override replace failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, gocb.Cas(newCas))
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", newCas)})
			return
		}

		if in.MatchCAS == nil && !in.CreateOnly && (overrideUpsertWithExpiry != nil || overrideUpsert != nil) {
			if overrideUpsertWithExpiry != nil {
				var ttlSeconds int64
				if in.Expiry != nil {
					ttlSeconds = int64((*in.Expiry) / time.Second)
				}
				cas, err := overrideUpsertWithExpiry(bucket, scope, collection, id, in.Doc, ttlSeconds)
				if err != nil {
					status := mapErrorToStatus(err)
					if status >= 500 {
						if logger != nil {
							logger.Error("override upsert failed", zap.Error(err))
						}
					} else {
						if logger != nil {
							logger.Warn("override upsert failed", zap.Error(err))
						}
					}
					response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
					return
				}
				setETag(w, gocb.Cas(cas))
				response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
				return
			}
			cas, err := overrideUpsert(bucket, scope, collection, id, in.Doc)
			if err != nil {
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("override upsert failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("override upsert failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
			return
		}

		// Resolve collection
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

		if in.CreateOnly {
			cas, err := doCreateOnly(coll, id, in.Doc, in.Expiry, r.Context())
			if err != nil {
				if errors.Is(err, gocb.ErrDocumentExists) {
					if logger != nil {
						logger.Warn("create-only conflict", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.String("op", "insert"))
					}
					response.JSON(w, http.StatusConflict, map[string]any{"code": "conflict_exists", "message": "document already exists"})
					return
				}
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("insert failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("insert failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, cas)
			response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(cas))})
			return
		}

		if in.MatchCAS != nil {
			cas, err := doReplaceCAS(coll, id, in.Doc, *in.MatchCAS, in.Expiry, r.Context())
			if err != nil {
				if errors.Is(err, gocb.ErrCasMismatch) {
					if logger != nil {
						logger.Warn("CAS mismatch on replace", zap.String("id", id), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.Uint64("expected_cas", uint64(*in.MatchCAS)), zap.String("op", "replace"))
					}
					response.JSON(w, http.StatusConflict, map[string]any{"code": "cas_mismatch", "message": "cas mismatch"})
					return
				}
				if errors.Is(err, gocb.ErrDocumentNotFound) {
					response.JSON(w, http.StatusNotFound, map[string]any{"code": "not_found", "message": "document not found"})
					return
				}
				status := mapErrorToStatus(err)
				if status >= 500 {
					if logger != nil {
						logger.Error("replace failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("replace failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, cas)
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(cas))})
			return
		}

		cas, err := doUnconditionalUpsert(coll, id, in.Doc, in.Expiry, r.Context())
		if err != nil {
			status := mapErrorToStatus(err)
			if status >= 500 {
				if logger != nil {
					logger.Error("upsert failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("upsert failed", zap.Error(err))
				}
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
			return
		}
		setETag(w, cas)
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(cas))})
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

		// request size limit
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20)

		var bodyMap map[string]any
		if r.Body != nil {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				low := strings.ToLower(err.Error())
				if strings.Contains(low, "request body too large") {
					response.JSON(w, http.StatusRequestEntityTooLarge, map[string]any{"code": "too_large", "message": "request body too large"})
					return
				}
				response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid request body"})
				return
			}
			if len(bytes.TrimSpace(raw)) > 0 {
				if err := json.Unmarshal(raw, &bodyMap); err != nil {
					response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid JSON body"})
					return
				}
			}
		}

		// Strict CAS resolution
		var headerCASSet bool
		var headerCAS string
		if rawIfMatch := strings.TrimSpace(r.Header.Get("If-Match")); rawIfMatch != "" {
			if strings.HasPrefix(rawIfMatch, "W/") {
				response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid If-Match header"})
				return
			}
			if !(len(rawIfMatch) >= 2 && rawIfMatch[0] == '"' && rawIfMatch[len(rawIfMatch)-1] == '"') {
				response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid If-Match header"})
				return
			}
			headerCAS = rawIfMatch[1 : len(rawIfMatch)-1]
			headerCASSet = true
		}

		var bodyCASPtr *string
		if v, ok := bodyMap["if_match_cas"]; ok {
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) == "" {
				response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "if_match_cas must be a quoted string"})
				return
			}
			s = strings.TrimSpace(s)
			bodyCASPtr = &s
		}

		var matchCASPtr *gocb.Cas
		switch {
		case headerCASSet && bodyCASPtr != nil && headerCAS != *bodyCASPtr:
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "conflicting CAS in header and body"})
			return
		case headerCASSet:
			cas, err := parseCASString(headerCAS)
			if err != nil {
				response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid If-Match header"})
				return
			}
			matchCASPtr = &cas
		case !headerCASSet && bodyCASPtr != nil:
			cas, err := parseCASString(*bodyCASPtr)
			if err != nil {
				response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid if_match_cas"})
				return
			}
			matchCASPtr = &cas
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
					if status >= 500 {
						if logger != nil {
							logger.Error("override delete failed", zap.Error(err))
						}
					} else {
						if logger != nil {
							logger.Warn("override delete failed", zap.Error(err))
						}
					}
					response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
					return
				}
				setETag(w, gocb.Cas(oldCas))
				response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", oldCas)})
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
				if status >= 500 {
					if logger != nil {
						logger.Error("delete failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("delete failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
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
				if status >= 500 {
					if logger != nil {
						logger.Error("override delete failed", zap.Error(err))
					}
				} else {
					if logger != nil {
						logger.Warn("override delete failed", zap.Error(err))
					}
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", cas)})
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
		res, err := coll.Remove(id, &gocb.RemoveOptions{Context: r.Context()})
		if err != nil {
			status := mapErrorToStatus(err)
			if status >= 500 {
				if logger != nil {
					logger.Error("delete failed", zap.Error(err))
				}
			} else {
				if logger != nil {
					logger.Warn("delete failed", zap.Error(err))
				}
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
			return
		}
		setETag(w, res.Cas())
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
	}
}
