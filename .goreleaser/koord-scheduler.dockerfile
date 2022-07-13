FROM gcr.io/distroless/static:latest
WORKDIR /
COPY koord-scheduler .
ENTRYPOINT ["/koord-scheduler"]
