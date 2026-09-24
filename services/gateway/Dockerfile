# Multi-stage build: compile with the full Go toolchain, run on a minimal
# Alpine image (not fully distroless) so the container has wget for its own
# Docker healthcheck, per docker-compose.yml.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY pkg ./pkg
COPY services/gateway ./services/gateway
RUN --mount=type=cache,target=/root/.cache/go-build     --mount=type=cache,target=/go/pkg/mod     CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w"     -o /out/gateway ./services/gateway/cmd

FROM alpine:3.22
RUN addgroup -S app && adduser -S app -G app
COPY --from=build /out/gateway /gateway
USER app
EXPOSE 8080
ENTRYPOINT ["/gateway"]
