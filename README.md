## Couchbase-backed REST API Scaffold (Go 1.25)

Production-ready scaffold for a REST API using:
- Go 1.25
- Couchbase Go SDK v2.11.0
- chi router
- Uber zap logging
- Env config with validation
- Swagger tooling (ready to generate once endpoints are added)

### Layout

```
cmd/api/main.go
internal/config/config.go
internal/logger/logger.go
internal/couchbase/client.go
internal/http/{server.go,middleware.go,routes.go}
internal/errors/errors.go
pkg/response/response.go
```

### Configuration

Environment variables (see `.env.example`):
- APP_ENV: dev|prod
- LOG_LEVEL: debug|info|warn|error|dpanic|panic|fatal
- SERVER_PORT, timeouts
- CORS_ALLOWED_ORIGINS
- CB_CONN_STR, CB_USERNAME, CB_PASSWORD, CB_BUCKET, CB_TIMEOUT

### Development

```
make tidy
make dev
```

If you have `air` installed, `make dev` uses it; otherwise `go run`.

### Testing

```
make test
```

### Swagger

Install swag: `go install github.com/swaggo/swag/cmd/swag@latest`

Generate docs (no annotated endpoints yet):
```
make docs
```

Open Swagger UI at `/swagger/index.html` after you add endpoints and annotations.

### Docker

```
docker build -t couchbase-go:latest .
docker run --rm -p 8080:8080 --env-file .env couchbase-go:latest
```

### Notes
- No API endpoints are implemented yet; `internal/http/routes.go` has a placeholder.
- Couchbase client connects on startup; add operations through repositories/services later.


