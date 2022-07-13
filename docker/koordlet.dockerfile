FROM golang:1.17 as builder
WORKDIR /go/src/github.com/koordinator-sh/koordinator

COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download

COPY apis/ apis/
COPY cmd/ cmd/
COPY pkg/ pkg/

RUN GOOS=linux GOARCH=amd64 go build -a -o koordlet cmd/koordlet/main.go

FROM nvidia/cuda:11.6.1-base-ubuntu20.04
WORKDIR /
COPY --from=builder /go/src/github.com/koordinator-sh/koordinator/koordlet .
ENTRYPOINT ["/koordlet"]
