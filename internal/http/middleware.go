package http

import (
	"net"
	stdhttp "net/http"
	"strings"
	"time"

	chi_middleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/pkg/response"
)

// LoggingMiddleware logs request details using zap.

func LoggingMiddleware(logger *zap.Logger) func(next stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			start := time.Now()
			rw := chi_middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			requestID := chi_middleware.GetReqID(r.Context())
			if requestID != "" {
				rw.Header().Set("X-Request-ID", requestID)
			}
			next.ServeHTTP(rw, r)
			dur := time.Since(start)

			logger.Info("http_request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rw.Status()),
				zap.Int("bytes", rw.BytesWritten()),
				zap.Duration("duration", dur),
				zap.String("request_id", requestID),
				zap.String("remote_ip", getRemoteIP(r)),
				zap.String("host", r.Host),
				zap.String("user_agent", r.UserAgent()),
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
					requestID := chi_middleware.GetReqID(r.Context())
					logger.Error("panic recovered", zap.Any("error", rec), zap.String("request_id", requestID), zap.Stack("stack"))
					response.JSON(w, stdhttp.StatusInternalServerError, map[string]any{
						"error": map[string]any{
							"code":       stdhttp.StatusText(stdhttp.StatusInternalServerError),
							"message":    "unexpected server error",
							"request_id": requestID,
						},
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func getRemoteIP(r *stdhttp.Request) string {
	if xfwd := r.Header.Get("X-Forwarded-For"); xfwd != "" {
		parts := strings.Split(xfwd, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	if rip := r.Header.Get("X-Real-IP"); rip != "" {
		return rip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
