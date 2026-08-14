# Build of the upstream document service. Static binary on an empty base: no
# shell, no package manager, and nothing in the image that could be talked into
# making an authorization decision.

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/document-service .

FROM scratch
COPY --from=build /out/document-service /document-service
# The service binds the container port only. In the compose stack it is reached
# through Envoy; it is published to the host purely so the README can show what
# the service does when nothing is guarding it.
EXPOSE 8080
ENTRYPOINT ["/document-service"]
