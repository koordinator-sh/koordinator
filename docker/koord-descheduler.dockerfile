FROM --platform=$TARGETPLATFORM golang:1.20 as builder
WORKDIR /go/src/github.com/koordinator-sh/koordinator

ARG VERSION
ARG TARGETARCH
ENV VERSION $VERSION
ENV GOOS linux
ENV GOARCH $TARGETARCH

COPY . . 

RUN CGO_ENABLED=0 go build -a -o koord-descheduler cmd/koord-descheduler/main.go

FROM --platform=$TARGETPLATFORM gcr.io/distroless/static:latest
WORKDIR /
COPY --from=builder /go/src/github.com/koordinator-sh/koordinator/koord-descheduler .
ENTRYPOINT ["/koord-descheduler"]
