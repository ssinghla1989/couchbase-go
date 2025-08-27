package http

import (
	"github.com/go-chi/chi/v5"
	httpSwagger "github.com/swaggo/http-swagger"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/internal/health"
	"github.com/ssinghl/couchbase-go/internal/http/handlers"
)

// RegisterRoutes registers API routes on the router.
// Intentionally left empty for now; only mounts Swagger UI.
func RegisterRoutes(r *chi.Mux, cbClient *couchbase.Client, logger *zap.Logger) {
	// Swagger UI at /swagger/index.html
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	// Health endpoints for LB/Kubernetes
	r.Get("/health/liveness", health.LivenessHandler)
	r.Get("/health/readiness", health.ReadinessHandler(cbClient, logger))

	// Document CRUD endpoints (no /api/v1 prefix per requirements)
	r.Get("/buckets/{bucket}/docs/{id}", handlers.GetDocument(cbClient, logger))
	r.Post("/buckets/{bucket}/docs", handlers.CreateDocument(cbClient, logger))
	r.Put("/buckets/{bucket}/docs/{id}", handlers.UpsertDocument(cbClient, logger))
	r.Delete("/buckets/{bucket}/docs/{id}", handlers.DeleteDocument(cbClient, logger))

	// Bulk operations
	r.Post("/buckets/{bucket}/docs/_bulk_get", handlers.BulkGet(cbClient, logger))
	r.Post("/buckets/{bucket}/docs/_bulk_upsert", handlers.BulkUpsert(cbClient, logger))

	// N1QL query endpoint
	r.Post("/query", handlers.QueryHandler(cbClient, logger))
}
