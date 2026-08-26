# Build context is the repo root, not eventing-demo/receiver -- same
# reasoning as ui/Dockerfile: this is a subpackage of the knative-ctl
# Go module (module knative-ctl, no separate go.mod here).
#   docker build -f eventing-demo/receiver/Dockerfile .
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY eventing-demo/receiver ./eventing-demo/receiver
RUN CGO_ENABLED=0 go build -o /out/receiver ./eventing-demo/receiver

FROM alpine:3.20
RUN adduser -D -u 1000 app
COPY --from=build /out/receiver /usr/local/bin/receiver
USER 1000
ENTRYPOINT ["/usr/local/bin/receiver"]
