# syntax=docker/dockerfile:1
# SPDX-License-Identifier: Apache-2.0
FROM golang:1.26-alpine@sha256:8ac98ca534ac3f51e1f420a1ddc15e74c75cfa0f23f3ad27eb5d7236c349a0c AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
# TAGS=ebpf_full builds the agent with the real eBPF loader; run `make ebpf`
# first so the compiled objects are in the build context.
ARG TAGS=""
RUN CGO_ENABLED=0 go build -o /out/backend ./backend/cmd && CGO_ENABLED=0 go build -tags "$TAGS" -o /out/agent ./agent/cmd

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS backend
COPY --from=build /out/backend /backend
EXPOSE 8080
ENTRYPOINT ["/backend"]

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS agent
COPY --from=build /out/agent /agent
EXPOSE 8081
ENTRYPOINT ["/agent"]

# Root variant for the privileged eBPF path (needs CAP_BPF as uid 0).
# Build with TAGS=ebpf_full (after `make ebpf`): the same /out/agent binary.
FROM gcr.io/distroless/static@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0 AS agent-ebpf
COPY --from=build /out/agent /agent
EXPOSE 8081
ENTRYPOINT ["/agent"]
