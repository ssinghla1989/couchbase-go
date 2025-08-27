## Couchbase-backed REST API (Go 1.25)

Production-grade REST API using:
- Go 1.25
- Couchbase Go SDK v2.11.0 (single cluster)
- chi router
- Uber zap logging
- caarlos0/env + validator
- Swagger via http-swagger

### Layout

```
cmd/api/main.go
internal/config/config.go
internal/logger/logger.go
internal/couchbase/client.go
internal/http/{server.go,middleware.go,routes.go}
internal/http/handlers/{docs.go,query.go}
internal/health/{handlers.go}
internal/errors/errors.go
pkg/response/response.go
```

### Endpoints (root mounted)

- Health:
  - GET `/health/liveness` → `{ "status": "alive" }`
  - GET `/health/readiness` → `{ "ready": true, "cluster": "..." }` or 503 `{ "ready": false, "error": "..." }`
- CRUD (optional `scope`, `collection` query params):
  - GET `/buckets/{bucket}/docs/{id}`
  - POST `/buckets/{bucket}/docs` (body JSON, optional `prefix` for ID)
  - PUT `/buckets/{bucket}/docs/{id}` (body JSON)
  - DELETE `/buckets/{bucket}/docs/{id}`
- Query:
  - POST `/query` with body: `{ "statement": "SELECT ... WHERE x=$x", "params": {"x":1}, "readonly": true }`

All handlers return JSON with consistent fields like `id`, `cas`, `content`, `deleted`. Errors use safe messages and include `request_id` in panic responses via middleware.

### Configuration

Environment variables (see `.env.example`):
- APP_ENV: e0|e1|e2|e3 (e0=local, e1=dev, e2=qa, e3=prod)
- LOG_LEVEL: debug|info|warn|error|dpanic|panic|fatal
- SERVER_PORT, REQUEST_TIMEOUT, SERVER_READ_TIMEOUT, SERVER_READ_HEADER_TIMEOUT, SERVER_WRITE_TIMEOUT, SERVER_IDLE_TIMEOUT, SHUTDOWN_TIMEOUT
- CORS_ALLOWED_ORIGINS: CSV list or `*`
- Single-cluster Couchbase:
  - CB_CONN_STR, CB_USERNAME, CB_PASSWORD, CB_BUCKET
  - CB_KV_DIAL_TIMEOUT_MS, CB_KV_OP_TIMEOUT_MS

Load order:
1. `.env` (base, optional)
2. `.env.<APP_ENV>` (environment-specific: `.env.e0`, `.env.e1`, `.env.e2`, `.env.e3`)
3. Actual environment variables (highest precedence)

### Dev and DX

```
make tidy
make dev
```
- Request logging: method, path, status, bytes, duration, request_id
- Recoverer: logs stack, returns 500 JSON with `request_id`
- CORS: CSV parsing with safe defaults; credentials only when origins are explicit

### Testing

```
make test-race
```

### Swagger

Install swag: `go install github.com/swaggo/swag/cmd/swag@latest`

Generate docs:
```
make docs
```
Open Swagger UI at `/swagger/index.html`.

### Docker

```
docker build -t couchbase-go:latest .
docker run --rm -p 8080:8080 --env-file .env.e0 couchbase-go:latest
```
- Multi-stage build, non-root runtime, `GOMEMLIMIT` set (override per deploy)

### Examples

- GET a document:
```
curl -s http://localhost:8080/buckets/users/docs/user::1
```
- CREATE a document:
```
curl -s -X POST "http://localhost:8080/buckets/users/docs?prefix=user::" -H 'Content-Type: application/json' -d '{"name":"Alice"}'
```
- QUERY (parameterized):
```
curl -s -X POST http://localhost:8080/query -H 'Content-Type: application/json' \
  -d '{"statement":"SELECT name FROM `users` WHERE id=$id","params":{"id":"user::1"}}'
```

### Notes
- Secrets are never logged; only placeholders in `.env.example`.
- Endpoints remain at root (no `/api/v1`).


