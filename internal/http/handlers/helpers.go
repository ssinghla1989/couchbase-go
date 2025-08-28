package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/couchbase/gocb/v2"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// parseCASString parses a decimal string into gocb.Cas.
func parseCASString(s string) (gocb.Cas, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return gocb.Cas(0), fmt.Errorf("invalid cas")
	}
	u, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return gocb.Cas(0), fmt.Errorf("invalid cas")
	}
	return gocb.Cas(u), nil
}

// setETag writes an ETag header with the CAS quoted.
func setETag(w http.ResponseWriter, cas gocb.Cas) {
	if w == nil {
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", uint64(cas)))
}

// getScopeAndCollection extracts optional scope/collection from query params.
func getScopeAndCollection(r *http.Request) (string, string) {
	scope := r.URL.Query().Get("scope")
	collection := r.URL.Query().Get("collection")
	return scope, collection
}

// resolveScopeAndCollection merges body-provided scope/collection with query params and defaults.
func resolveScopeAndCollection(bodyScope, bodyCollection string, r *http.Request) (string, string) {
	scope := bodyScope
	collection := bodyCollection
	if scope == "" {
		scope = r.URL.Query().Get("scope")
	}
	if collection == "" {
		collection = r.URL.Query().Get("collection")
	}
	if scope == "" {
		scope = "_default"
	}
	if collection == "" {
		collection = "_default"
	}
	return scope, collection
}

// mapErrorToStatus maps SDK and generic errors to HTTP status codes.
func mapErrorToStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
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

//
