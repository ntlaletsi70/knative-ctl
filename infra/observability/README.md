# Tracing: Zipkin

One Zipkin instance receiving spans from everything a request or event
passes through, so a single trace shows the whole path rather than one
component's view of it.

```
knative-ctl install zipkin        # also part of `install all`
kubectl port-forward -n observability svc/zipkin 9411:9411
# http://localhost:9411
```

## What reports to it

`install zipkin` deploys `zipkin.yaml`, then patches three ConfigMaps —
three, because three separate things emit spans:

| ConfigMap | Covers |
| --- | --- |
| `knative-serving/config-observability` | activator, and the `queue-proxy` sidecar in every revision pod |
| `knative-serving/config-kourier` | Kourier's Envoy gateway — the first hop for both east-west and north-south traffic |
| `knative-eventing/config-observability` | broker ingress, broker filter, in-memory channel dispatcher (only if Eventing is installed) |

Each gets `tracing-protocol: http/protobuf`, `tracing-endpoint:
http://zipkin.observability.svc.cluster.local:9411/v1/traces` and a
sampling rate. The activator, Kourier controller and Eventing data
plane are restarted to pick it up; revision pods aren't — `queue-proxy`
reads its config when the pod is created, so a revision that was
already running starts reporting after its next scale-from-zero.

Order doesn't matter: `install kourier` and `install eventing` re-apply
the tracing config themselves when Zipkin is already there, and the
patched keys survive re-applying the upstream manifests (those only
ship an `_example` block, so `kubectl apply` leaves keys it never set
alone).

## Why not the stock Zipkin image

Knative used to export Zipkin's own format (`config-tracing`, `backend:
zipkin`). That's gone: as of `knative-v1.23` `config-tracing` is a
deprecation stub and every component exports OTLP only. Stock
`openzipkin/zipkin` doesn't accept OTLP.

`zipkin.yaml` runs `ghcr.io/openzipkin-contrib/zipkin-otel` instead —
the same server and UI, plus an OTLP/HTTP collector on the same port
(`POST :9411/v1/traces`). One pod, where the alternative is stock
Zipkin with an OpenTelemetry Collector in front of it translating.

## Watching traffic live

The [dashboard](../../ui/) has a live traffic panel built on this —
per-revision traffic share and the most recent requests — and serves
Zipkin's UI itself under `/zipkin/`.

In Zipkin's own UI (`http://localhost:9411` through the port-forward):

- **Find a trace → Run Query** lists the most recent traces; narrow with
  `serviceName=` to one hop. Re-run it while a load test is going.
- **Dependencies** draws the call graph Zipkin has observed, with call
  and error counts per edge.

Or from a terminal, the same data over the API:

```
curl -s 'localhost:9411/api/v2/services'
curl -s 'localhost:9411/api/v2/traces?limit=5&lookback=60000' | jq '.[] | map(.localEndpoint.serviceName + " " + .name)'
curl -s "localhost:9411/api/v2/dependencies?endTs=$(date +%s000)"
```

## What a trace looks like

One `GET` to `knative-demo-app`, as Zipkin recorded it on a fresh
`install all` (service name, span name):

```
kourier-knative    ingress                 the gateway, first hop
activator          get                     in the path because the revision can scale to zero
activator          throttler_try
activator          activator_proxy
activator          get /
knative-demo-app   get                     queue-proxy, reporting under the Service's own name
knative-demo-app   kn.queueproxy.proxy
knative-demo-app   http get                queue-proxy -> the app container
```

Most of what the query page lists, though, will be single-span
`kourier-knative` traces well under a millisecond long. Those are
Knative's own readiness probes against the gateway, not traffic. Filter
them out with `minDuration` or by picking any other `serviceName`.

## Sampling

Starts at `1` (every request) — right for a demo, where the one request
you just sent is the one you want to see. Under real load it isn't:

```
knative-ctl trace-sampling 0.1
```

## The webhook → Broker hop

A trace only stays in one piece if every hop passes the W3C
`traceparent` header along. Knative's own components do. The demo's
`github-receiver` is the one hop that makes a *new* outbound request
(to the Broker), so it copies `traceparent`/`tracestate` from the
request it received onto the one it sends
(`eventing-demo/receiver/main.go`). Without that, the webhook's arrival
and its fan-out to subscribers show up as two unrelated traces.

## Limits

- **In-memory storage.** Traces are lost when the pod restarts, and
  capped at `MEM_MAX_SPANS` (50000).
- **No spans from inside the apps.** The demo app and `echo-logger`
  aren't instrumented, so each trace ends at that pod's `queue-proxy`.
- **No authentication.** Zipkin has no Ingress of its own, but the
  dashboard proxies its UI, so anything that can reach the dashboard
  can read every trace. Same caveat as the dashboard itself
  (`ui/README.md`).
