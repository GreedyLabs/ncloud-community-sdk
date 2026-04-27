SHELL := /usr/bin/env bash
.PHONY: help gen gen-sdk gen-cli build test clean

help:  ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?##"}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# ── codegen ───────────────────────────────────────────────────────────
# Order matters: SDK is regenerated from spec, then CLI from spec+SDK.
# Run `make gen` after any spec edit.

gen-sdk:  ## Regenerate sdk/services/*.gen.go from spec/
	cd sdk/gen && bash regen.sh
	python3 sdk/gen/build_endpoints.py

gen-cli:  ## Regenerate cli/services/*.gen.go from spec/ + sdk/
	python3 cli/tools/build_cli_typed.py

gen: gen-sdk gen-cli  ## Regenerate everything (SDK then CLI)

# ── build / test ─────────────────────────────────────────────────────

build:  ## Build SDK + CLI
	cd sdk && go build ./...
	cd cli && go build ./...

test:  ## Run SDK + CLI tests
	cd sdk && go test ./...
	cd cli && go test ./...

clean:  ## Drop bin/
	rm -rf cli/bin/

# ── lint ─────────────────────────────────────────────────────────────

lint:  ## Spec lint + go vet for SDK + CLI
	cd spec && make lint
	cd sdk && go vet ./...
	cd cli && go vet ./...
