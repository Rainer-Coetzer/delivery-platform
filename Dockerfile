# One Dockerfile for every binary in the repo. CMD picks what to build:
#   ./cmd/platform              (default: the whole platform, `pilot` profile)
#   ./services/order/cmd        (one microservice, `full` profile)
#
# Multi-stage: compile with the Go toolchain, run on minimal Alpine (not
# fully distroless) so the container has wget for its healthcheck.
FROM golang:1.26-alpine AS build
ARG CMD=./cmd/platform
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY pkg ./pkg
COPY services ./services
COPY cmd ./cmd
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ${CMD}

FROM alpine:3.22
RUN addgroup -S app && adduser -S app -G app
COPY --from=build /out/app /app
USER app
ENTRYPOINT ["/app"]
