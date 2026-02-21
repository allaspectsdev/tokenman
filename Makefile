BINARY := tokenman
MODULE := github.com/allaspects/tokenman
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION) \
           -X $(MODULE)/internal/version.GitCommit=$(COMMIT) \
           -X $(MODULE)/internal/version.BuildDate=$(DATE)

.PHONY: build test lint clean run docker-build docker-run

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/tokenman

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/
	go clean -cache

run: build
	./bin/$(BINARY) start --foreground

docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t tokenman:$(VERSION) \
		-t tokenman:latest .

docker-run:
	docker run --rm -it \
		-p 7677:7677 -p 7678:7678 \
		-v tokenman-data:/data \
		tokenman:latest
