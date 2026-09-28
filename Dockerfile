# syntax=docker/dockerfile:1
# SPDX-License-Identifier: Apache-2.0
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
# TAGS=ebpf_full builds the agent with the real eBPF loader; run `make ebpf`
# first so the compiled objects are in the build context.
ARG TAGS=""
RUN CGO_ENABLED=0 go build -o /out/backend ./backend/cmd && CGO_ENABLED=0 go build -tags "$TAGS" -o /out/agent ./agent/cmd

FROM gcr.io/distroless/static:nonroot AS backend
COPY --from=build /out/backend /backend
EXPOSE 8080
ENTRYPOINT ["/backend"]

FROM gcr.io/distroless/static:nonroot AS agent
COPY --from=build /out/agent /agent
EXPOSE 8081
ENTRYPOINT ["/agent"]

# Root variant for the privileged eBPF path (needs CAP_BPF as uid 0).
# Build with TAGS=ebpf_full (after `make ebpf`): the same /out/agent binary.
FROM gcr.io/distroless/static AS agent-ebpf
COPY --from=build /out/agent /agent
EXPOSE 8081
ENTRYPOINT ["/agent"]
