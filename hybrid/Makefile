# Image URL to use all building/pushing image targets
CONTROLLER_IMG ?= koordinator/hybrid-controller:latest
COLLECTOR_IMG ?= koordinator/hybrid-collector:v0.1.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)



##@ Build
.PHONY: build
build:  ## Build hybrid-manager binary.
	go build -o bin/hybrid-manager ./cmd/hybrid-manager/main.go

.PHONY: run-manager
run-manager:  ## Run hybrid-manager from your host.
	go run ./cmd/hybrid-manager/main.go --prediction-file=examples/prediction-result.csv


.PHONY: run-collector
run-collector:  ## Run collector from your host.
	go run ./cmd/hybrid-collector/main.go --config=./deploy/config/collector.yaml



.PHONY: docker-build
docker-build: ## Build docker image for controller.
	docker build -t ${CONTROLLER_IMG} .

.PHONY: docker-build-collector
docker-build-collector: ## Build docker image for collector.
	docker build -t ${COLLECTOR_IMG} -f collector/Dockerfile collector/

.PHONY: docker-build-all
docker-build-all: docker-build## Build all docker images.

.PHONY: docker-push
docker-push: ## Push docker image for collector and controller.
	docker push ${CONTROLLER_IMG} && docker push ${COLLECTOR_IMG}




##@ Deployment

.PHONY: deploy
deploy: ## Deploy controller to the K8s cluster.
	kubectl apply -f ./deploy/manifests/hybrid-collector.yaml
	kubectl apply -f ./deploy/manifests/hybrid-controller.yaml


.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster.
	kubectl delete -f ./deploy/manifests/hybrid-collector.yaml
	kubectl delete -f ./deploy/manifests/hybrid-controller.yaml

