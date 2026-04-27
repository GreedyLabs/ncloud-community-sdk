SHELL := /usr/bin/env bash
.PHONY: help build test install lint clean gen

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X github.com/greedylabs/ncloud-community-cli/cmd.version=$(VERSION) \
           -X github.com/greedylabs/ncloud-community-cli/cmd.commit=$(COMMIT) \
           -X github.com/greedylabs/ncloud-community-cli/cmd.date=$(DATE)

help:  ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?##"}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build:  ## Compile to ./bin/ncc
	@mkdir -p bin
	go build -ldflags="$(LDFLAGS)" -o bin/ncc .

test:  ## Run unit tests
	go test ./...

install:  ## Install to $GOBIN/ncc (or $GOPATH/bin)
	go install -ldflags="$(LDFLAGS)" .

lint:  ## go vet
	go vet ./...

clean:  ## Remove build artefacts
	rm -rf bin/

gen:  ## Regenerate services/*.gen.go from the spec catalog
	python3 tools/build_cli_ops.py
