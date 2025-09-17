FROM golang:1.20 as build

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

# Build
RUN go build -a -o koord-device-daemon cmd/koord-device-daemon/main.go

FROM --platform=$TARGETPLATFORM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends libpfm4 pciutils && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /

COPY --from=build /go/src/github.com/koordinator-sh/koordinator/koord-device-daemon .

ENTRYPOINT ["/koord-device-daemon"]
