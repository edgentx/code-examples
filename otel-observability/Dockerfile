# One Dockerfile, two binaries: the compose file passes SERVICE=frontdesk or
# SERVICE=records so both images come out of the same build.
FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG SERVICE
RUN test -n "${SERVICE}" && \
    CGO_ENABLED=0 go build -trimpath -o /out/agency-service ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/agency-service /agency-service
USER nonroot:nonroot
ENTRYPOINT ["/agency-service"]
