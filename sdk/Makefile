SHELL := /usr/bin/env bash
.PHONY: help submodule gen build test all

help:  ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?##"}{printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

submodule:  ## Initialize / update the spec submodule
	git submodule update --init --recursive

gen: submodule  ## Regenerate per-service Go clients via oapi-codegen
	cd gen && bash regen.sh

build:  ## Build the SDK
	go build ./...

test:  ## Run unit tests
	go test ./...

all: gen build test  ## End-to-end check
