# ncloud-community-spec — pure OpenAPI data + viewer.
#
# This repo contains spec data only. It depends on no internal tooling — only
# upstream `openapi-spec-validator` for the lint check. Audit / quality
# regression checks live in the (private) ncloud-community-tools repo and run
# there before opening PRs back to this repo.
SHELL := /usr/bin/env bash
PYTHON ?= python3
# Specs live under tier-prefixed subdirs (services/<tier>/<slug>.yaml).
# The flat services/*.yaml layout is gone; this glob has to walk one
# level deeper or lint silently skips all 78 service files.
SPECS := $(wildcard services/*/*.yaml)

.PHONY: help lint site all

help:  ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?##"}{printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

lint:  ## OpenAPI 3.0.3 lint every spec
	@pass=0; fail=0; \
	for f in openapi.yaml $(SPECS); do \
	  $(PYTHON) -c "from openapi_spec_validator import validate; import yaml; validate(yaml.safe_load(open('$$f')))" 2>/dev/null \
	    && pass=$$((pass+1)) || { echo "FAIL $$f"; fail=$$((fail+1)); }; \
	done; \
	echo "lint: $$pass pass, $$fail fail"; \
	[ $$fail -eq 0 ]

site:  ## Print viewer-serving instructions
	@echo "Serve viewer/ locally:"
	@echo "  python3 -m http.server -d . 8080"
	@echo "Then open http://localhost:8080/viewer/"

all: lint  ## Default check
