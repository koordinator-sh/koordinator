FROM nvidia/cuda:11.2.2-base-ubuntu20.04
WORKDIR /
COPY koordlet .
ENTRYPOINT ["/koordlet"]
