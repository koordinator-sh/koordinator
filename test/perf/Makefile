# test/perf/Makefile
# Run from repo root: make -C test/perf <target>
# Or from test/perf/: make <target>

SCENARIO   ?= basic
CONFIG     ?= configs/scenarios/$(SCENARIO)-1k.yaml
OUTPUT     ?= results/result.json
KUBECONFIG ?= $(HOME)/.kube/config
BASELINE   ?=

# Prefer an annotated tag; fall back to short commit SHA.
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
VERSION_FLAG := -X github.com/koordinator-sh/koordinator/test/perf/pkg/framework.Version=$(GIT_VERSION)

.PHONY: benchmark setup teardown clean test help

## Run a single scenario benchmark. Override CONFIG directly to use any
## config file, e.g. `make benchmark CONFIG=configs/scenarios/basic-100.yaml`
benchmark:
	@echo "Running scenario: $(SCENARIO)"
	go run -ldflags="$(VERSION_FLAG)" ./cmd/benchmark/main.go \
		--config    $(CONFIG) \
		--output    $(OUTPUT) \
		--kubeconfig $(KUBECONFIG) \
		$(if $(BASELINE),--baseline $(BASELINE),)

## Set up a local kind cluster with kwok + Koordinator
setup:
	./hack/setup-cluster.sh

## Tear down the local kind cluster
teardown:
	./hack/teardown-cluster.sh

## Remove result files
clean:
	rm -rf results/

## Run unit tests
test:
	go test ./...

## Show available targets
help:
	@grep -E '^##' Makefile | sed 's/## //'
