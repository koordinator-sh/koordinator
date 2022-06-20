# Build the manager binary
FROM golang:1.17-alpine as builder

ARG MODULE

WORKDIR /go/src/github.com/koordinator-sh/koordinator
RUN apk add --update make git bash rsync gcc musl-dev

# Copy the go source
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY apis/ apis/
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build
ENV GOOS linux
ENV GOARCH amd64
RUN go build -a -o ${MODULE} /go/src/github.com/koordinator-sh/koordinator/cmd/${MODULE}/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM alpine:3.12
RUN apk add --update bash net-tools iproute2 logrotate less rsync util-linux
WORKDIR /
ARG MODULE
COPY --from=builder /go/src/github.com/koordinator-sh/koordinator/${MODULE} .
