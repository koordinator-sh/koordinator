# test/perf/Makefile
# Run from repo root: make -C test/perf <target>
# Or from test/perf/: make <target>

SCENARIO ?= basic
OUTPUT   ?= results/result.json
KUBECONFIG ?= $(HOME)/.kube/config

.PHONY: benchmark setup teardown clean help

## Run a single scenario benchmark
benchmark:
	@echo "Running scenario: $(SCENARIO)"
	go run ./cmd/benchmark/main.go \
		--config  configs/scenarios/$(SCENARIO)-1k.yaml \
		--output  $(OUTPUT) \
		--kubeconfig $(KUBECONFIG)

## Set up a local kind cluster with kwok + Koordinator
setup:
	./hack/setup-cluster.sh

## Tear down the local kind cluster
teardown:
	./hack/teardown-cluster.sh

## Remove result files
clean:
	rm -rf results/

## Show available targets
help:
	@grep -E '^##' Makefile | sed 's/## //'