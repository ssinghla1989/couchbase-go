package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	overrideGet    func(bucket, scope, collection, id string) (uint64, map[string]any, error)
	overrideInsert func(bucket, scope, collection, id string, body json.RawMessage) (uint64, error)
	overrideUpsert func(bucket, scope, collection, id string, body json.RawMessage) (uint64, error)
	overrideDelete func(bucket, scope, collection, id string) (uint64, error)
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
		res, err := coll.Get(id, nil)
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
			response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
			return
		}

		coll, err := collectionFor(cb, bucket, scope, collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}
		res, err := coll.Insert(id, body, nil)
		if err != nil {
			status := mapErrorToStatus(err)
			response.Error(w, status, err)
			return
		}
		response.JSON(w, http.StatusCreated, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
	}
}

// UpsertDocument handles insert or replace of a document by ID.
// @Summary Upsert document
// @Description Upsert (insert or replace) a document by ID
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id} [put]
func UpsertDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")
		scope, collection := getScopeAndCollection(r)

		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}

		if overrideUpsert != nil {
			cas, err := overrideUpsert(bucket, scope, collection, id, body)
			if err != nil {
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", cas)})
			return
		}

		coll, err := collectionFor(cb, bucket, scope, collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}
		res, err := coll.Upsert(id, body, nil)
		if err != nil {
			status := mapErrorToStatus(err)
			response.Error(w, status, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
	}
}

// DeleteDocument handles deletion of a document by ID.
// @Summary Delete document
// @Description Delete a document by ID
// @Param bucket path string true "Bucket name"
// @Param id path string true "Document ID"
// @Param scope query string false "Scope name"
// @Param collection query string false "Collection name"
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/docs/{id} [delete]
func DeleteDocument(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")
		scope, collection := getScopeAndCollection(r)

		if overrideDelete != nil {
			cas, err := overrideDelete(bucket, scope, collection, id)
			if err != nil {
				status := mapErrorToStatus(err)
				response.Error(w, status, err)
				return
			}
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", cas)})
			return
		}

		coll, err := collectionFor(cb, bucket, scope, collection)
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, err)
			return
		}
		res, err := coll.Remove(id, nil)
		if err != nil {
			status := mapErrorToStatus(err)
			response.Error(w, status, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
	}
}
