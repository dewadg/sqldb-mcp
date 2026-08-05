BINARY   := sqldb-mcp
CMD      := ./cmd/sqldb-mcp
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w \
            -X main.version=$(VERSION) \
            -X main.commit=$(COMMIT) \
            -X main.buildDate=$(BUILD_DATE)

# Optimization flags for final production builds.
GOFLAGS  := -trimpath -buildvcs=false
CGO_ENABLED := 0

.PHONY: build clean run test vet

## build: compile an optimized, static production binary into ./bin
build:
	CGO_ENABLED=$(CGO_ENABLED) go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

## clean: remove build artifacts
clean:
	rm -rf bin

## run: build and run the server (stdio transport)
run: build
	./bin/$(BINARY)

## test: run the test suite
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...
