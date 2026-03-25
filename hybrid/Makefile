# Copyright 2026 The KubeGien Authors. All rights reserved.
# Use of this source code is governed by a Apache license
# that can be found in the LICENSE file.

TAG ?= v1.0

.PHONY: binary
binary: hybrid-collector hybrid-manager

# Build flags for debugging
#FLAGS=-gcflags "all=-N -l"
# Build flags for product
FLAGS=-ldflags="-s -w"

hybrid-collector:
	CGO_ENABLED=0 go build ${FLAGS}  -o bin/hybrid-collector cmd/hybrid-collector/main.go

hybrid-manager:
	CGO_ENABLED=0 go build ${FLAGS} -o bin/hybrid-manager cmd/hybrid-manager/main.go


binary: hybrid-collector hybrid-manager

clean:
	rm -f bin/*

.PHONY: all-images
all-images: binary collector-image manager-image

.PHONY: container
container: all-images

collector-image:
	docker build -f ./build/hybrid-collector/Dockerfile -t hybrid-system/hybrid-collector:$(TAG) .

manager-image:
	docker build -f ./build/hybrid-manager/Dockerfile -t hybrid-system/hybrid-manager:$(TAG) .
