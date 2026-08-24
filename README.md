# knative-ctl

A small standalone Go CLI for a Knative Serving + Kourier proof-of-concept:
platform install/uninstall, plus canary and blue-green release automation
built directly on `ksvc.spec.traffic` revision splitting (no extra
controller like Argo Rollouts/Flagger required).

## Build

```
go build -o knative-ctl .
```

## Platform lifecycle

Installs/uninstalls against the cluster the current kubeconfig points at,
by applying the upstream release manifests (defaults to `knative-v1.23.0`).

```
knative-ctl install   knative|kourier|all [--version knative-vX.Y.Z]
knative-ctl uninstall knative|kourier|all [--version knative-vX.Y.Z]
```

`install kourier` also configures Kourier as the default Knative ingress
class. Applies are retried once on transient failure (CRD establishment can
briefly time out under load).

## Example manifests

Each folder is a self-contained, directly `kubectl apply`-able sequence for
one deployment pattern. All three carry the same autoscaling annotations,
which are what make scale-to-zero and on-demand scale-up through the
activator work (see inline comments in the manifests for what each one
does):

- `autoscaling.knative.dev/class: kpa.autoscaling.knative.dev` — the
  Knative Pod Autoscaler; unlike the `hpa` class it can scale to 0.
- `autoscaling.knative.dev/min-scale: "0"` — allows scaling to zero. At 0
  replicas the activator sits in the request path, buffers the incoming
  request, and triggers a cold-start scale-up on demand.
- `autoscaling.knative.dev/max-scale: "3"` — capped low deliberately for
  small/resource-constrained clusters.
- `autoscaling.knative.dev/target: "10"` and
  `target-utilization-percentage: "70"` — scale-out trigger (concurrent
  in-flight requests per pod, with headroom).
- `autoscaling.knative.dev/scale-down-delay: "30s"` — hysteresis so a
  brief lull doesn't immediately scale back down.

**`examples/normal/`** — a plain single-revision service, 100% traffic by
default (`spec.traffic` omitted):

```
kubectl apply -f examples/normal/service.yaml
```

**`examples/canary/`** — three-step progressive traffic shift, run in
order (or drive the same flow with `knative-ctl canary` instead of
applying by hand):

```
kubectl apply -f examples/canary/01-stable.yaml
kubectl apply -f examples/canary/02-canary-split.yaml   # 80/20 split
kubectl apply -f examples/canary/03-promote.yaml        # 100% to v2, v1 kept at 0%
```

**`examples/bluegreen/`** — deploy dark, then cut over atomically (or use
`knative-ctl bluegreen`):

```
kubectl apply -f examples/bluegreen/01-blue.yaml
kubectl apply -f examples/bluegreen/02-green-cutover.yaml
```

## Release flows

Operate on a Knative Service that's already deployed.

**Canary** — shift traffic to a new revision in steps, tagging both
revisions (`stable`/`canary`) for individual testability along the way:

```
knative-ctl canary <service> <image> [--steps 10,50,100] [--interval 20s] [--namespace default]
```

**Blue-green** — deploy the new revision dark (0% traffic), wait for it to
become Ready, then cut over atomically. The previous revision is retained
at 0% for instant rollback:

```
knative-ctl bluegreen <service> <image> [--namespace default]
```

**Rollback** — flip 100% traffic to any named revision:

```
knative-ctl rollback <service> <revision> [--namespace default]
```
