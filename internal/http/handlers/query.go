package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// test override hooks
var (
	overrideQuery func(statement string, params map[string]any, readonly bool) (rows []map[string]any, metadata map[string]any, err error)
)

type queryRequest struct {
	Statement string         `json:"statement"`
	Params    map[string]any `json:"params"`
}

func isParameterized(statement string) bool {
	// naive check: must contain at least one $param token
	return strings.Contains(statement, "$")
}

// isSelectStatement returns true if the statement appears to be a SELECT query.
func isSelectStatement(statement string) bool {
	s := strings.TrimSpace(strings.ToLower(statement))
	// skip any leading parenthesis
	for strings.HasPrefix(s, "(") {
		s = strings.TrimSpace(s[1:])
	}
	return strings.HasPrefix(s, "select")
}

// QueryHandler executes a parameterized N1QL query.
// @Summary Execute N1QL query
// @Description Run parameterized N1QL query (readonly by default)
// @Accept json
// @Produce json
// @Param request body queryRequest true "Query request"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /query [post]
func QueryHandler(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req queryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
			return
		}
		if req.Statement == "" || !isParameterized(req.Statement) {
			response.Error(w, http.StatusBadRequest, errors.New("statement must be parameterized with $params"))
			return
		}
		// enforce read-only and SELECT-only queries
		if !isSelectStatement(req.Statement) {
			response.Error(w, http.StatusBadRequest, errors.New("only SELECT queries are allowed"))
			return
		}
		readonly := true

		// allow test override
		if overrideQuery != nil {
			rows, meta, err := overrideQuery(req.Statement, req.Params, readonly)
			if err != nil {
				status := mapErrorForQuery(err)
				response.Error(w, status, err)
				return
			}
			response.JSON(w, http.StatusOK, map[string]any{"results": rows, "metadata": meta})
			return
		}

		opts := &gocb.QueryOptions{
			Adhoc:    true,
			Readonly: readonly,
			Timeout:  10 * time.Second,
			Context:  r.Context(),
		}
		if len(req.Params) > 0 {
			opts.NamedParameters = req.Params
		}
		qr, err := cb.Cluster().Query(req.Statement, opts)
		if err != nil {
			status := mapErrorForQuery(err)
			response.Error(w, status, err)
			return
		}
		writeQueryResponse(w, qr)
	}
}

func mapErrorForQuery(err error) int {
	if err == nil {
		return http.StatusOK
	}
	// map common connectivity issues to 503
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "network") || strings.Contains(msg, "unavailable") {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadRequest
}

func writeQueryResponse(w http.ResponseWriter, qr *gocb.QueryResult) {
	var rows []map[string]any
	for qr.Next() {
		var row map[string]any
		_ = qr.Row(&row)
		rows = append(rows, row)
	}
	meta, _ := qr.MetaData()
	response.JSON(w, http.StatusOK, map[string]any{
		"results":  rows,
		"metadata": map[string]any{"metrics": meta.Metrics},
	})
}
