APP_NAME=couchbase-go
MAIN_PKG=./cmd/api

.PHONY: dev build test lint docs tidy tidy-up mod-download

dev:
	# Prefer air if installed, else fallback to go run
	@command -v air >/dev/null 2>&1 && air || go run $(MAIN_PKG)

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/$(APP_NAME) $(MAIN_PKG)

test:
	go test ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed; skipping."
	go vet ./...

docs:
	@command -v swag >/dev/null 2>&1 && swag init -g cmd/api/main.go -o ./docs || echo "swag not installed; skipping docs gen."

tidy:
	go mod tidy

mod-download:
	go mod download


