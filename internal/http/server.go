package http

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chi_middleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/config"
)

// NewRouter creates the chi router with base middleware and empty routes.
func NewRouter(cfg *config.Config, logger *zap.Logger) *chi.Mux {
	r := chi.NewRouter()

	// Standard middleware
	r.Use(chi_middleware.RequestID)
	r.Use(Recoverer(logger))
	r.Use(LoggingMiddleware(logger))
	r.Use(chi_middleware.Timeout(cfg.RequestTimeout))

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{cfg.CORSAllowedOrigins},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	return r
}

// HTTPServer wraps the standard library server.
type HTTPServer struct {
	Server *stdhttp.Server
}

// NewServer constructs the HTTP server using provided router and config.
func NewServer(cfg *config.Config, router stdhttp.Handler) *HTTPServer {
	return &HTTPServer{
		Server: &stdhttp.Server{
			Addr:         fmt.Sprintf(":%d", cfg.ServerPort),
			Handler:      router,
			ReadTimeout:  cfg.ServerReadTimeout,
			WriteTimeout: cfg.ServerWriteTimeout,
			IdleTimeout:  cfg.ServerIdleTimeout,
		},
	}
}

// Start runs the server in a goroutine.
func (s *HTTPServer) Start() {
	go func() {
		_ = s.Server.ListenAndServe()
	}()
}

// Shutdown gracefully stops the server.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
	}
	return s.Server.Shutdown(ctx)
}
