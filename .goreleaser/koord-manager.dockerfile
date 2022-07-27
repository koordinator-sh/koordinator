FROM gcr.io/distroless/static:latest
WORKDIR /
COPY koord-manager .
ENTRYPOINT ["/koord-manager"]
