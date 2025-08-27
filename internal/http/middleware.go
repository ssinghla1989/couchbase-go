package http

import (
	stdhttp "net/http"
	"time"

	chi_middleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// LoggingMiddleware logs request details using zap.

func LoggingMiddleware(logger *zap.Logger) func(next stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			start := time.Now()
			rw := chi_middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(rw, r)
			dur := time.Since(start)

			requestID := chi_middleware.GetReqID(r.Context())
			logger.Info("http_request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rw.Status()),
				zap.Int("bytes", rw.BytesWritten()),
				zap.Duration("duration", dur),
				zap.String("request_id", requestID),
			)
		})
	}
}

// Recoverer recovers from panics and logs the error and stacktrace.

func Recoverer(logger *zap.Logger) func(next stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered", zap.Any("error", rec), zap.Stack("stack"))
					w.WriteHeader(stdhttp.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
