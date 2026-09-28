# SPDX-License-Identifier: Apache-2.0
# Pinned eBPF compile toolchain: clang + llvm + libbpf headers.
# The probe is CO-RE (no kernel headers needed), so this image only
# provides the compiler, not target kernel sources.
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
    clang llvm libbpf-dev linux-libc-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    # linux-libc-dev ships arch UAPI under <triplet>/asm but no top-level
    # <asm/...> symlink; clang -target bpf needs the latter.
    && ln -s /usr/include/*-linux-gnu/asm /usr/include/asm
WORKDIR /src
