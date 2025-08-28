APP_NAME=couchbase-go
MAIN_PKG=./cmd/api

.PHONY: dev build test test-race lint fmt vet tidy tidy-up mod-download staticcheck gosec docker

dev:
	# Prefer air if installed, else fallback to go run
	@command -v air >/dev/null 2>&1 && air || go run $(MAIN_PKG)

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/$(APP_NAME) $(MAIN_PKG)

test:
	go test ./...

test-race:
	go test ./... -race -count=1 -coverprofile=coverage.out

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed; skipping."
	$(MAKE) vet

staticcheck:
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed; skipping."

gosec:
	@command -v gosec >/dev/null 2>&1 && gosec ./... || echo "gosec not installed; skipping."


tidy:
	go mod tidy -v
	go mod verify

mod-download:
	go mod download

# Build docker image
IMAGE_NAME?=$(APP_NAME):latest

docker:
	docker build -t $(IMAGE_NAME) .


