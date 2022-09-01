FROM golang:1.17 as builder
WORKDIR /go/src/github.com/koordinator-sh/koordinator

COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

COPY apis/ apis/
COPY cmd/ cmd/
COPY pkg/ pkg/

RUN GOOS=linux GOARCH=amd64 go build -a -o koordlet cmd/koordlet/main.go

# The CUDA container images provide an easy-to-use distribution for CUDA supported platforms and architectures.
# NVIDIA provides rich images in https://hub.docker.com/r/nvidia/cuda/tags, literally cover all kinds of CUDA version
# and system architecture. Please replace the following base image according to your Kubernetes/System environment.
# For more details about how those images got built, you might wanna check the original Dockerfile in
# https://gitlab.com/nvidia/container-images/cuda/-/tree/master/dist.

FROM nvidia/cuda:11.2.2-base-ubuntu20.04
WORKDIR /
COPY --from=builder /go/src/github.com/koordinator-sh/koordinator/koordlet .
ENTRYPOINT ["/koordlet"]
