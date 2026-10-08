# The container uses the host Go toolchain.
FROM ubuntu:24.04
RUN apt-get update \
    && apt-get install -y --no-install-recommends qemu-user-static gcc-s390x-linux-gnu libc6-dev-s390x-cross gcc \
    && rm -rf /var/lib/apt/lists/*
