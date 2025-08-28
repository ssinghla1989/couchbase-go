package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/couchbase/gocb/v2"
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
