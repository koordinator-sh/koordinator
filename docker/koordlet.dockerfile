FROM --platform=$TARGETPLATFORM golang:1.20 as builder
WORKDIR /go/src/github.com/koordinator-sh/koordinator

ARG VERSION
ARG TARGETARCH
ENV VERSION $VERSION
ENV GOOS linux
ENV GOARCH $TARGETARCH

RUN apt update && apt install -y bash build-essential cmake wget
RUN wget https://sourceforge.net/projects/perfmon2/files/libpfm4/libpfm-4.13.0.tar.gz && \
  echo "bcb52090f02bc7bcb5ac066494cd55bbd5084e65  libpfm-4.13.0.tar.gz" | sha1sum -c && \
  tar -xzf libpfm-4.13.0.tar.gz && \
  rm libpfm-4.13.0.tar.gz && \
  export DBG="-g -Wall" && \
  make -e -C libpfm-4.13.0 && \
  make install -C libpfm-4.13.0

COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

COPY apis/ apis/
COPY cmd/ cmd/
COPY pkg/ pkg/

RUN go build -a -o koordlet cmd/koordlet/main.go

# The CUDA container images provide an easy-to-use distribution for CUDA supported platforms and architectures.
# NVIDIA provides rich images in https://hub.docker.com/r/nvidia/cuda/tags, literally cover all kinds of CUDA version
# and system architecture. Please replace the following base image according to your Kubernetes/System environment.
# For more details about how those images got built, you might wanna check the original Dockerfile in
# https://gitlab.com/nvidia/container-images/cuda/-/tree/master/dist.

FROM --platform=$TARGETPLATFORM nvidia/cuda:11.8.0-base-ubuntu22.04
WORKDIR /
RUN apt-get update && apt-get install -y lvm2 iptables pciutils && rm -rf /var/lib/apt/lists/*
COPY --from=builder /go/src/github.com/koordinator-sh/koordinator/koordlet .
COPY --from=builder /usr/local/lib /usr/lib
ENTRYPOINT ["/koordlet"]
