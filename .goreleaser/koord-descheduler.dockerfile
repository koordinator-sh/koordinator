FROM gcr.io/distroless/static:latest
WORKDIR /
COPY koord-descheduler .
ENTRYPOINT ["/koord-descheduler"]
