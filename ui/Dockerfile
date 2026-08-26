# Build context is the repo root (not ui/), since this needs both Go
# binaries: knative-ui itself, and knative-ctl, which it shells out to for
# releases -- same "one place for release logic" reasoning as ui/README.md.
#   docker build -f ui/Dockerfile .
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY ui ./ui
RUN CGO_ENABLED=0 go build -o /out/knative-ctl .
RUN CGO_ENABLED=0 go build -o /out/knative-ui ./ui

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    apk add --no-cache --virtual .fetch curl && \
    curl -fsSL -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/$(curl -fsSL https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && \
    chmod +x /usr/local/bin/kubectl && \
    apk del .fetch && \
    adduser -D -u 1000 app

COPY --from=build /out/knative-ctl /usr/local/bin/knative-ctl
COPY --from=build /out/knative-ui /usr/local/bin/knative-ui
COPY ui/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

USER 1000
WORKDIR /home/app
ENTRYPOINT ["/entrypoint.sh"]
