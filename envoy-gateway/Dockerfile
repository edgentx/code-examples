# One Dockerfile builds both upstreams; SERVICE selects which command.
#
#   docker build --build-arg SERVICE=service-a -t envoy-gateway-service-a .
#
# The image carries no shell-visible source and no toolchain, and the process
# runs as an unprivileged user. Neither upstream needs to bind a low port or
# read a certificate, so neither needs any privilege at all.

FROM golang:1.24-alpine AS build
ARG SERVICE
WORKDIR /src
# No third-party dependencies, so there is nothing to download and nothing to
# vendor: go.mod alone is the whole dependency story.
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN test -n "${SERVICE}" || (echo 'build-arg SERVICE is required' >&2; exit 1)
RUN CGO_ENABLED=0 go build -trimpath -o /out/upstream "./cmd/${SERVICE}"

FROM alpine:3.20
RUN adduser -D -u 10001 upstream
COPY --from=build /out/upstream /usr/local/bin/upstream
USER 10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/upstream"]
