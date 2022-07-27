FROM nvidia/cuda:11.6.1-base-ubuntu20.04
WORKDIR /
COPY koordlet .
ENTRYPOINT ["/koordlet"]
