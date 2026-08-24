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

## Example demo service

```
kubectl apply -f examples/helloworld-go.yaml
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
