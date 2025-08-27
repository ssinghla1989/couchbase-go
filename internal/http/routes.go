package http

import (
	"github.com/go-chi/chi/v5"
	httpSwagger "github.com/swaggo/http-swagger"
)

// RegisterRoutes registers API routes on the router.
// Intentionally left empty for now; only mounts Swagger UI.
func RegisterRoutes(r *chi.Mux) {
	// Swagger UI at /swagger/index.html
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	// TODO: add API endpoints here in future.
}
