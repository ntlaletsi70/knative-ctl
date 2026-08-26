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

## Scope

Runs locally against whatever cluster your kubeconfig points at, for
now — not deployed into the cluster or exposed behind the
`infra/ingress/` front door. That's a reasonable next step once this is
proven out, but wasn't the starting scope.
