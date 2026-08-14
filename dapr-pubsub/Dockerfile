# One Dockerfile, two services: SERVICE selects which cmd/ package to build.
FROM golang:1.24-alpine AS build
ARG SERVICE
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/${SERVICE}

FROM alpine:3.21
RUN adduser -D -u 10001 app
COPY --from=build /out/service /usr/local/bin/service
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/service"]
