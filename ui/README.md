# Dashboard

A small local web UI on top of `knative-ctl`: trigger canary/bluegreen/
rollback releases from a form, and watch pods scale up/down live as they
happen. Doesn't reimplement release logic — it shells out to the
`knative-ctl` binary itself and streams its output, same as running it
from a terminal.

```
go build -o knative-ctl .      # from the repo root, if not already built
go build -o knative-ui ./ui
./knative-ui                    # http://127.0.0.1:8090
```

Flags: `--addr` (default `127.0.0.1:8090`, localhost-only), `--knative-ctl`
(path to the binary, default `./knative-ctl`).

## How it works

- `GET /api/pods/stream?namespace=knative-demo` — Server-Sent Events,
  polls `kubectl get pods -n <namespace> -o json` once a second and
  pushes a snapshot. The frontend diffs snapshots by pod name to animate
  new/removed pods, which is what actually shows scale-up/scale-down as
  it happens — Knative's cold starts and scale-to-zero are seconds-scale,
  well within a 1s poll's resolution.
- `GET /api/release/stream?action=canary|bluegreen|rollback&service=...` —
  SSE, runs `knative-ctl <action> ...` as a subprocess with the given
  args (built as an argv slice, never through a shell, so there's no
  injection surface regardless of what a client sends) and streams
  combined stdout/stderr line by line, then an `event: done` with the
  exit status.

No client-go, no new dependencies (stdlib `net/http` + `embed` only) —
same "shell out to kubectl" approach as the rest of this repo, and
consistent with `main.go`/`release.go` not linking against client-go
either.

## Running in-cluster

Also deployable as a Knative Service itself, reachable through the same
north-south path as the demo app (`infra/ingress/`):

```
kubectl apply -f ui/rbac.yaml
kubectl apply -f ui/service.yaml
```

`rbac.yaml` is a ServiceAccount + Role + RoleBinding scoped to
`knative-demo` only — `pods` get/list/watch (the pods panel),
`services.serving.knative.dev` get/list/watch/patch and
`revisions.serving.knative.dev` get/list/watch (what `knative-ctl`'s
release flows touch). `service.yaml` pins `min-scale`/`max-scale` to
`1` — an ops dashboard cold-starting, and losing its live pod-watch
connection every time it scales to zero, is bad UX, unlike the demo app
where scale-to-zero is the point. No pinned revision name in the
template: this manifest gets re-applied with new image tags as the
dashboard changes, and a pinned name would make Knative's webhook
reject that as an illegal mutation of an immutable revision — the same
bug `release.go`'s `deployNewRevision` works around for `knative-ctl`'s
own release flows.

`ui/Dockerfile` builds both `knative-ui` and `knative-ctl` (which it
shells out to) plus `kubectl` into one image; build context is the repo
root, not `ui/`:

```
docker build -f ui/Dockerfile .
```

`.github/workflows/build-ui.yml` does this in CI and pushes to
`ghcr.io`, mirroring `build-app.yml`. `ui/entrypoint.sh` builds
a kubeconfig from the pod's mounted ServiceAccount token at startup —
`kubectl` (unlike client-go) has no automatic in-cluster mode, so this
is the standard way to point the plain CLI at the API server from
inside a pod.

**No authentication.** Anything that can reach the dashboard's URL can
trigger a real release (`canary`/`bluegreen`/`rollback` all just work,
no login) or watch pod state. Fine for a private LAN + Tailscale mesh
PoC where reaching it at all already means you're on the tailnet or
LAN — not something to expose more broadly without adding auth first.

## Running locally instead

Doesn't need the cluster deployment above — runs directly against
whatever cluster your kubeconfig points at:

```
go build -o knative-ctl .      # from the repo root, if not already built
go build -o knative-ui ./ui
./knative-ui                    # http://127.0.0.1:8090
```

Flags: `--addr` (default `127.0.0.1:8090`, localhost-only), `--knative-ctl`
(path to the binary, default `./knative-ctl`).
