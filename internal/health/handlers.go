package health

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// readinessCheckOverride allows tests to stub readiness checks.
var readinessCheckOverride func(ctx context.Context, timeout time.Duration) error

// connStrOverride allows tests to stub the cluster connection string.
var connStrOverride func() string

// LivenessHandler always returns HTTP 200 with a simple alive status.
// GET /api/v1/health/liveness
func LivenessHandler(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, http.StatusOK, map[string]any{"status": "alive"})
}

// ReadinessHandler checks Couchbase client readiness using WaitUntilReady.
// GET /api/v1/health/readiness
func ReadinessHandler(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		// Use a conservative timeout for readiness checks
		checkTimeout := 2 * time.Second

		var err error
		if readinessCheckOverride != nil {
			err = readinessCheckOverride(ctx, checkTimeout)
		} else if cb != nil {
			err = cb.WaitUntilReady(ctx, checkTimeout)
		} else {
			err = context.DeadlineExceeded
		}

		if err != nil {
			if logger != nil {
				logger.Warn("couchbase readiness check failed", zap.Error(err))
			}
			response.JSON(w, http.StatusServiceUnavailable, map[string]any{
				"ready": false,
				"error": err.Error(),
			})
			return
		}

		clusterStr := ""
		if connStrOverride != nil {
			clusterStr = connStrOverride()
		} else if cb != nil {
			clusterStr = cb.ConnStr()
		}

		response.JSON(w, http.StatusOK, map[string]any{
			"ready":   true,
			"cluster": clusterStr,
		})
	}
}
