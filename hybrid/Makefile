# Copyright 2026 The KubeGien Authors. All rights reserved.
# Use of this source code is governed by a Apache license
# that can be found in the LICENSE file.

TAG ?= v1.2
ARCH ?= amd64

AMD64_IMAGE = hybrid-system/hybrid-agent:$(TAG)-amd64
ARM64_IMAGE = hybrid-system/hybrid-agent:$(TAG)-arm64

# Build flags for debugging
#FLAGS=-gcflags "all=-N -l"
# Build flags for product
FLAGS=-ldflags="-s -w"

# Cross-compile a local binary for the target arch (no container required)
.PHONY: binary hybrid-agent
binary: hybrid-agent

hybrid-agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build $(FLAGS) -o bin/hybrid-agent cmd/hybrid-agent/main.go

# Build the container image using multi-stage Docker build.
# Compilation happens inside the builder stage; no local binary needed.
.PHONY: agent-image
agent-image:
	docker build \
		--platform linux/$(ARCH) \
		--build-arg TARGETARCH=$(ARCH) \
		-f ./build/hybrid-agent/Dockerfile \
		-t hybrid-system/hybrid-agent:$(TAG)-$(ARCH) \
		.

# Build amd64 image shortcut
.PHONY: agent-image-amd64
agent-image-amd64:
	$(MAKE) agent-image ARCH=amd64

# Build arm64 image shortcut
.PHONY: agent-image-arm64
agent-image-arm64:
	$(MAKE) agent-image ARCH=arm64

.PHONY: all-images container
all-images: agent-image-amd64 agent-image-arm64
	@echo "\nBuilt images:"
	@echo "  $(AMD64_IMAGE)"
	@echo "  $(ARM64_IMAGE)"
container: all-images

.PHONY: clean
clean:
	rm -f bin/*
