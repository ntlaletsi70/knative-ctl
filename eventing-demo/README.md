# Knative Eventing demo: GitHub webhook → Broker/Trigger fan-out

A real GitHub webhook, delivered over the public internet via
`tailscale funnel`, converted to a CloudEvent, routed through a
Knative `Broker`, and fanned out by `Trigger` filters to independent
subscribers.

```
GitHub ──HTTPS──▶ tailscale funnel ──▶ nginx-ingress (NodePort)
                                              │ Ingress rewrites Host to
                                              │ the cluster-local name
                                              ▼
                                      kourier-internal (ClusterIP)
                                              ▼
                                      github-receiver (Knative Service)
                                              │ wraps as CloudEvent,
                                              │ POSTs to the Broker
                                              ▼
                                    Broker "default" (knative-demo)
                                        │              │           │
                              ce-type match     ce-type match  ce-type match
                              dev.github.push  .pull_request   .ping
                                        │              │           │
                                        ▼              ▼           ▼
                                push-handler      pr-handler   push-handler
```

## Why not Knative's `GitHubSource`

That's a whole extra controller to install on a cluster that's already
tight on CPU (see the top-level README's resource notes). `receiver/`
is ~100 lines of Go instead: parses the raw webhook, sets the
`ce-type`/`ce-id`/`ce-source` headers Knative expects (binary HTTP
content mode), forwards the original JSON body untouched.

## Components

- `receiver/` — accepts the raw GitHub POST, forwards to the Broker.
  Deployed `min-scale: 1` (needs to be reliably up, not cold-starting
  on the first delivery attempt).
- `echo-logger/` — generic subscriber, just logs what it receives.
  Backs both `push-handler` and `pr-handler` — what each one actually
  gets is decided entirely by its `Trigger`'s `ce-type` filter, not by
  anything different in the image.
- `manifests/` — `Broker`, the two subscriber `Service`s, three
  `Trigger`s (push, pull_request, and ping — GitHub's own webhook-setup
  test event, routed so it's visibly handled rather than silently
  dropped), and the webhook's public `Ingress`.

## The public route

Kourier is `ClusterIP`-only in this repo (see
[`infra/ingress/`](../infra/ingress/)), and `kourier-internal` only
knows a Knative Service by its cluster-local name
(`github-receiver.knative-demo.svc.cluster.local`). GitHub, though,
calls this node's Tailscale MagicDNS hostname.

`04-webhook-ingress.yaml` bridges the two on nginx-ingress: it matches
the public hostname and rewrites the `Host` header to the cluster-local
one (`nginx.ingress.kubernetes.io/upstream-vhost`), so the webhook
takes the same nginx → `kourier-internal` path as all other
north-south traffic. This replaced an earlier `DomainMapping`, which
only works through Kourier's external listener — the thing that's no
longer exposed — and needed `autocreate-cluster-domain-claims` patched
into `config-network` besides.

## Setting it up

```
knative-ctl install eventing                  # core + in-memory channel + mt channel broker
kubectl apply -f manifests/00-broker.yaml
kubectl apply -f manifests/01-subscribers.yaml
kubectl apply -f manifests/02-triggers.yaml
kubectl apply -f manifests/03-receiver.yaml
kubectl apply -f manifests/04-webhook-ingress.yaml   # set its host to your own MagicDNS name

tailscale funnel --bg 30412   # nginx-ingress's HTTP NodePort -- check yours:
                               #   kubectl get svc ingress-nginx-controller -n ingress-nginx
```

Images are built by `.github/workflows/build-eventing-demo.yml` and
pushed to `ghcr.io/ntlaletsi70/eventing-demo-{receiver,echo-logger}`;
the manifests reference `:latest`. See the top-level README's CI
section for what that means for picking up a new build.

## Verified

On a fresh `kind` cluster, after `knative-ctl install all` and
`knative-ctl install eventing`:

- Simulated `push` and `pull_request` payloads sent with `curl` to
  nginx-ingress with the public `Host` header (the path `tailscale
  funnel` delivers to): `202` from the Broker, and the event logged by
  the right subscriber — `push-handler` or `pr-handler` — scaled up from
  zero to take it.
- The whole hop sequence as **one** Zipkin trace, 39 spans: gateway →
  activator → `github-receiver` → `broker.ingress` → in-memory channel
  dispatcher → `broker.filter` (all three Triggers evaluated, one
  matching) → gateway → activator → `push-handler`. The subscriber's
  cold start is visible in it as a ~1.4s `throttler_try` span on the
  activator. See [`infra/observability/`](../infra/observability/).

Verified earlier, on the original k3s node and through the
`DomainMapping` route this replaced: a real GitHub webhook over
`tailscale funnel` — GitHub's automatic `ping` on webhook creation, and
a follow-up commit's real `push` delivery with the actual commit
SHA/author/file list intact. The `funnel` → nginx-ingress leg of the
current route has not been re-run against real GitHub yet.

## Resource footprint on this hardware

This is what actually answered "can this cluster take Knative
Eventing" (see the top-level README's own resource notes for the
2 CPU / 3.7GB baseline): Eventing core + MT Channel Broker +
InMemoryChannel is 9 pods on its own. Installing it alongside Serving +
Kourier pushed `kubectl describe node` to **89% CPU requests committed
(~1780m of ~2000m, ~220m headroom)**, with **swap essentially maxed
(511/511Mi)** and ~1.2Gi memory available. Stable and working at that
level — zero restarts on anything installed today — but genuinely at
the ceiling, not comfortable margin. Two concrete things this forced:

- Tekton + Shipwright (an earlier, abandoned direction this session)
  had to be fully uninstalled first — Kourier's own gateway pod hit a
  hard `Insufficient cpu` scheduling failure otherwise, unrelated to
  memory.
- Even after that, Kourier's gateway `Deployment` needed its own CPU
  *request* trimmed from `200m` to `100m` by hand to close the last
  ~25m gap and let it schedule at all.

`push-handler`/`pr-handler` staying `min-scale: 0` (scaling to zero
between events) is load-bearing here, not just a nicety — it's part of
what keeps this fitting at all. Adding another always-on subscriber or
any heavier workload would likely need something else removed first,
the same trade this session already made once.
