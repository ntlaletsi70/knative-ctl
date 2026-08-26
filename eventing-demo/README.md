# Knative Eventing demo: GitHub webhook → Broker/Trigger fan-out

A real GitHub webhook, delivered over the public internet via
`tailscale funnel`, converted to a CloudEvent, routed through a
Knative `Broker`, and fanned out by `Trigger` filters to independent
subscribers.

```
GitHub ──HTTPS──▶ tailscale funnel ──▶ Kourier (external NodePort)
                                              │ DomainMapping routes
                                              │ this hostname to:
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
  dropped), and a `DomainMapping`.

## The DomainMapping gotcha

Knative's auto-generated `*.svc.cluster.local` route is cluster-local
only — Kourier's *external* Service 404s it (only `kourier-internal`
serves cluster-local routes; a known gotcha from early in this whole
PoC). `tailscale funnel` needs the *external* path, so
`04-domainmapping.yaml` maps this node's Tailscale MagicDNS hostname
directly to `github-receiver`, which gets it a real externally-routable
route instead.

That also needed `autocreate-cluster-domain-claims: "true"` patched
into `config-network` (defaults to `false` — meant for multi-tenant
clusters where an admin manually delegates domains to namespaces;
irrelevant on a single-tenant dev box) — without it, the
`DomainMapping` sits at `DomainAlreadyClaimed`/`False` forever, since
Knative expects a matching `ClusterDomainClaim` object to already
exist and won't create one itself.

## Setting it up

```
kubectl apply -f manifests/00-broker.yaml
kubectl apply -f manifests/01-subscribers.yaml
kubectl apply -f manifests/02-triggers.yaml
kubectl apply -f manifests/03-receiver.yaml   # update the image tags first, see below
kubectl patch configmap config-network -n knative-serving --type merge \
  -p '{"data":{"autocreate-cluster-domain-claims":"true"}}'
kubectl apply -f manifests/04-domainmapping.yaml

tailscale funnel --bg 31852   # 31852 is Kourier's external NodePort for :80 -- check yours:
                               #   kubectl get svc kourier -n kourier-system
```

Images are built by `.github/workflows/build-eventing-demo.yml`
(same `ghcr.io`+`ttl.sh` dual-push pattern as `build-app.yml`/
`build-ui.yml`) — update `manifests/01-subscribers.yaml` and
`manifests/03-receiver.yaml`'s image tags after a build.

## Verified

- Simulated `push`/`pull_request`/`ping` payloads via `curl`, both
  through `kourier-internal` (cluster-local) and through Kourier's
  external NodePort + the `DomainMapping` — correct fan-out each time,
  confirmed via each subscriber's logs.
- A real GitHub webhook configured on this repo
  (`https://api.github.com/repos/ntlaletsi70/knative-ctl/hooks`),
  pointed at the `tailscale funnel` URL. GitHub's automatic `ping`
  delivery on webhook creation arrived for real, over the actual
  public internet, and routed correctly to `push-handler` via
  `ping-trigger`.
