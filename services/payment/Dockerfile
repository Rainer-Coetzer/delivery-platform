# Multi-stage build: compile with the full Go toolchain, run on a minimal
# Alpine image (not fully distroless) so the container has wget for its own
# Docker healthcheck, per docker-compose.yml.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY pkg ./pkg
COPY services/payment ./services/payment
RUN --mount=type=cache,target=/root/.cache/go-build     --mount=type=cache,target=/go/pkg/mod     CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w"     -o /out/payment-service ./services/payment/cmd

FROM alpine:3.19
RUN addgroup -S app && adduser -S app -G app
COPY --from=build /out/payment-service /payment-service
USER app
EXPOSE 8084
ENTRYPOINT ["/payment-service"]
