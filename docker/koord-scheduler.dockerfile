FROM --platform=$TARGETPLATFORM golang:1.20 as builder
WORKDIR /go/src/github.com/koordinator-sh/koordinator

ARG VERSION
ARG TARGETARCH
ENV VERSION $VERSION
ENV GOOS linux
ENV GOARCH $TARGETARCH

COPY . .

RUN CGO_ENABLED=0 go build -a -o koord-scheduler cmd/koord-scheduler/main.go

FROM --platform=$TARGETPLATFORM gcr.io/distroless/static:latest
WORKDIR /
COPY --from=builder /go/src/github.com/koordinator-sh/koordinator/koord-scheduler .
ENTRYPOINT ["/koord-scheduler"]
