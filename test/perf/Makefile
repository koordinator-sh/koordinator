# test/perf/Makefile
# Run from repo root: make -C test/perf <target>
# Or from test/perf/: make <target>

SCENARIO   ?= basic
CONFIG     ?= configs/scenarios/$(SCENARIO)-1k.yaml
OUTPUT     ?= results/result.json
KUBECONFIG ?= $(HOME)/.kube/config
BASELINE   ?=

# Use the short commit SHA directly rather than git describe --dirty.
# hack/setup-cluster.sh runs hack/deploy_kind.sh, which rewrites tracked
# files under config/manager/ (kustomization.yaml unconditionally via
# "kustomize edit set image"; scheduler.yaml via sed on the feature-gates
# line when KUBERNETES_VERSION < 1.34), making the working tree
# deterministically dirty from the setup step onward. --dirty would
# therefore always fire, poisoning the koordinatorVersion field stamped
# into every baseline JSON.
GIT_VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
VERSION_FLAG := -X github.com/koordinator-sh/koordinator/test/perf/pkg/framework.Version=$(GIT_VERSION)

.PHONY: benchmark setup teardown clean test fmt-check help

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

## Fail if any .go file under test/perf is not gofmt-formatted
fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Not gofmt-formatted:"; echo "$$unformatted"; exit 1; \
	fi

## Run unit tests (includes format check)
test: fmt-check
	go test ./...

## Show available targets
help:
	@grep -E '^##' Makefile | sed 's/## //'
