package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/couchbase/gocb/v2"
)

// parseETagHeader extracts the unquoted value from a header of the form "\"123\"" or returns "*" as-is.
func parseETagHeader(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if s == "*" {
		return "*", true
	}
	if strings.HasPrefix(s, "W/") {
		s = strings.TrimPrefix(s, "W/")
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1], true
	}
	return s, true
}

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

// isCreateOnly returns true when the request indicates insert-only semantics.
func isCreateOnly(r *http.Request, bodyIfNoneMatch *bool) bool {
	if val, ok := parseETagHeader(r.Header.Get("If-None-Match")); ok {
		return val == "*"
	}
	if bodyIfNoneMatch != nil {
		return *bodyIfNoneMatch
	}
	return false
}

// getMatchCAS returns the CAS to match if provided via If-Match header or body field.
func getMatchCAS(r *http.Request, bodyCAS *string) (*gocb.Cas, error) {
	if etag, ok := parseETagHeader(r.Header.Get("If-Match")); ok && etag != "*" && etag != "" {
		cas, err := parseCASString(etag)
		if err != nil {
			return nil, err
		}
		return &cas, nil
	}
	if bodyCAS != nil && *bodyCAS != "" {
		cas, err := parseCASString(*bodyCAS)
		if err != nil {
			return nil, err
		}
		return &cas, nil
	}
	return nil, nil
}
