# syntax=docker/dockerfile:1
# SPDX-License-Identifier: Apache-2.0
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 go build -o /out/backend ./backend/cmd && CGO_ENABLED=0 go build -o /out/agent ./agent/cmd

FROM gcr.io/distroless/static:nonroot AS backend
COPY --from=build /out/backend /backend
EXPOSE 8080
ENTRYPOINT ["/backend"]

FROM gcr.io/distroless/static:nonroot AS agent
COPY --from=build /out/agent /agent
EXPOSE 8081
ENTRYPOINT ["/agent"]
