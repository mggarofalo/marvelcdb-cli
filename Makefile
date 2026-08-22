.PHONY: build install test test-cover lint vet fmt tidy clean docs

BINARY := marvelcdb
MODULE := github.com/mggarofalo/marvelcdb-cli
# Derived from the nearest tag so a source build reports a real version.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/marvelcdb

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/marvelcdb

test:
	go test ./... -race -count=1

test-cover:
	go test ./... -race -count=1 -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run

vet:
	go vet ./...

fmt:
	gofumpt -w .

tidy:
	go mod tidy

clean:
	rm -f $(BINARY) coverage.out coverage.html
	rm -rf dist
