# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM alpine:3.12
RUN apk add --update bash net-tools iproute2 logrotate less rsync util-linux
WORKDIR /
ARG MODULE
COPY ${MODULE} .
