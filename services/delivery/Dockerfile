# Multi-stage build: compile with the full Go toolchain, run on a minimal
# Alpine image (not fully distroless) so the container has wget for its own
# Docker healthcheck, per docker-compose.yml.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY pkg ./pkg
COPY services/delivery ./services/delivery
RUN --mount=type=cache,target=/root/.cache/go-build     --mount=type=cache,target=/go/pkg/mod     CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w"     -o /out/delivery-service ./services/delivery/cmd

FROM alpine:3.22
RUN addgroup -S app && adduser -S app -G app
COPY --from=build /out/delivery-service /delivery-service
USER app
EXPOSE 8085
ENTRYPOINT ["/delivery-service"]
