# Build stage: cross-compile for target arch (CGO disabled)
FROM golang:1.25-alpine AS builder

ARG TARGETARCH=amd64
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" \
    -o bin/hybrid-agent cmd/hybrid-agent/main.go

# Final stage: matches the compiled binary's target platform
FROM alpine:3.20

RUN apk add --no-cache tzdata && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone && \
    apk del tzdata

COPY --from=builder /app/bin/hybrid-agent /usr/local/bin/

WORKDIR /

USER 65532:65532

CMD ["/usr/local/bin/hybrid-agent"]
