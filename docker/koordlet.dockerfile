FROM golang:1.18 as builder
WORKDIR /go/src/github.com/koordinator-sh/koordinator

COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

COPY apis/ apis/
COPY cmd/ cmd/
COPY pkg/ pkg/

RUN apt update && apt install -y libpfm4 libpfm4-dev 
RUN GOOS=linux GOARCH=amd64 go build -tags=linux -a -o koordlet cmd/koordlet/main.go

# The CUDA container images provide an easy-to-use distribution for CUDA supported platforms and architectures.
# NVIDIA provides rich images in https://hub.docker.com/r/nvidia/cuda/tags, literally cover all kinds of CUDA version
# and system architecture. Please replace the following base image according to your Kubernetes/System environment.
# For more details about how those images got built, you might wanna check the original Dockerfile in
# https://gitlab.com/nvidia/container-images/cuda/-/tree/master/dist.

FROM nvidia/cuda:11.2.2-base-ubuntu20.04
WORKDIR /
RUN apt-get update && apt-get install -y lvm2 && rm -rf /var/lib/apt/lists/*
COPY --from=builder /go/src/github.com/koordinator-sh/koordinator/koordlet .
COPY --from=builder /usr/lib/x86_64-linux-gnu /usr/lib
ENTRYPOINT ["/koordlet"]
