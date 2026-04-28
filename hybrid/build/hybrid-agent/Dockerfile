FROM alpine:3.20

COPY ./bin/hybrid-agent /usr/local/bin/

WORKDIR /

USER 65532:65532

CMD ["/usr/local/bin/hybrid-agent"]
