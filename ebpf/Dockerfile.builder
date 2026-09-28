# SPDX-License-Identifier: Apache-2.0
# Pinned eBPF compile toolchain: clang + llvm + libbpf headers.
# The probe is CO-RE (no kernel headers needed), so this image only
# provides the compiler, not target kernel sources.
FROM ubuntu:24.04@sha256:008173c23f95b170204355c12626cb5a965d779a7e1283b09e9cffbb1bf33ca3
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    clang llvm libbpf-dev linux-libc-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    # linux-libc-dev ships arch UAPI under <triplet>/asm but no top-level
    # <asm/...> symlink; clang -target bpf needs the latter.
    && ln -s /usr/include/*-linux-gnu/asm /usr/include/asm
WORKDIR /src
