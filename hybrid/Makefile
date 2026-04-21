# Copyright 2026 The KubeGien Authors. All rights reserved.
# Use of this source code is governed by a Apache license
# that can be found in the LICENSE file.

TAG ?= v1.1

.PHONY: binary
binary: hybrid-agent

# Build flags for debugging
#FLAGS=-gcflags "all=-N -l"
# Build flags for product
FLAGS=-ldflags="-s -w"

hybrid-agent:
	CGO_ENABLED=0 go build ${FLAGS}  -o bin/hybrid-agent cmd/hybrid-agent/main.go

binary: hybrid-agent

.PHONY: all-images
all-images: binary agent-image

.PHONY: container
container: all-images

agent-image:
	docker build -f ./build/hybrid-agent/Dockerfile -t hybrid-system/hybrid-agent:$(TAG) .

manager-image:

clean:
	rm -f bin/*