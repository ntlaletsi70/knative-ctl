# Build context is the repo root -- see eventing-demo/receiver/Dockerfile.
#   docker build -f eventing-demo/echo-logger/Dockerfile .
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY eventing-demo/echo-logger ./eventing-demo/echo-logger
RUN CGO_ENABLED=0 go build -o /out/echo-logger ./eventing-demo/echo-logger

FROM alpine:3.20
RUN adduser -D -u 1000 app
COPY --from=build /out/echo-logger /usr/local/bin/echo-logger
USER 1000
ENTRYPOINT ["/usr/local/bin/echo-logger"]
