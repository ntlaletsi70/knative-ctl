# North-south: nginx-ingress + MetalLB + Kourier

Manifests for the pattern documented in the top-level README's
"North-south" section. Installed through `knative-ctl` itself, same
pattern as `knative`/`kourier` (apply with retry, wait for rollout) —
see `../../northsouth.go`. Order matters:

```
knative-ctl install all
```

runs the whole thing (knative, kourier, metallb, cert-manager, ingress)
in dependency order — or install pieces individually:

```
knative-ctl install kourier         # if not already installed
knative-ctl install metallb
knative-ctl install cert-manager
knative-ctl install ingress         # needs kourier; reverts its Service to ClusterIP
```

`metallb` and `cert-manager` stand alone and can run in either order
relative to each other, but both before `ingress` — its manifest
references cert-manager's TLS secret, and it needs Kourier's Service to
exist before overriding it. `--version` doesn't apply to these three;
`metallb`/`cert-manager` are pinned upstream release versions inside
`northsouth.go` (`metallbVersion`/`certManagerVersion` constants, not a
`--version` flag), and `ingress` doesn't have an upstream version at
all since it's a hand-edited file, not a raw fetch.

`metallb` and `cert-manager` are fetched straight from their upstream
release URLs, same as `knative`/`kourier` — no local copy checked in,
since neither is modified from stock. Only genuinely customized or
original content lives in this directory:

`ingress-nginx-baremetal-v1.11.3.yaml` is hand-edited from the stock
upstream release, not a raw download — three changes from the original:

- Controller `Service` type changed from `NodePort` to `LoadBalancer`,
  so it actually claims an external IP (from MetalLB's pool) instead of
  a random high port.
- `--default-backend-service=kourier-system/kourier-internal` added to
  the controller args — this is what makes unmatched traffic fall
  through to Kourier with the `Host` header intact, instead of nginx's
  stock 404 default backend.
- `--default-ssl-certificate=ingress-nginx/ingress-nginx-default-tls`
  added — the self-signed cert from `tls-selfsigned.yaml`, served for
  any HTTPS connection since there's no per-host `Ingress`/SNI config.
  Self-signed, no shared root: clients need `-k`/`--insecure` (or
  equivalent), nothing will trust it out of the box.

`metallb-pool.yaml` carves out `192.168.0.200-192.168.0.210` as the
MetalLB `IPAddressPool` — specific to this LAN, adjust for a different
network.

`kourier-clusterip.yaml` overrides Kourier's own `kourier` Service
(upstream default: `LoadBalancer`, which is what `knative-ctl install
kourier` applies) to `ClusterIP`, so it stops competing with
nginx-ingress for host ports 80/443. Apply it **after** installing/
reinstalling Kourier, not before — order matters, see the comment in
the file. This used to be a one-off `kubectl patch` run by hand, which
silently got reverted the moment `kourier.yaml` was ever re-applied
(kubectl apply's 3-way merge restores whatever the last manifest it
saw said) — now it's a real, re-appliable manifest instead of tribal
knowledge.

`kourier-northsouth-ingress.yaml` is the actual declared north-south
route: an `Ingress` object in `kourier-system`, host
`*.knative-demo.svc.cluster.local` (the same hosts
`tls-selfsigned.yaml`'s Certificate authenticates for), routing to
`kourier-internal`. Lives in `kourier-system` rather than
`ingress-nginx` because an `Ingress`'s backend `Service` must be in the
same namespace as the `Ingress` itself. This is what makes north-south
traffic reach Kourier, declaratively, rather than only through the
controller's own `--default-backend-service` flag (which stays too, as
a fallback for anything that doesn't match this host).

## Cluster-level step not captured in any manifest here

One change was made directly against node state, not via `kubectl
apply` of a file in this directory: **k3s's built-in `servicelb`
(klipper-lb) disabled**, alongside Traefik (already disabled earlier
in this PoC for the same reason — see the top-level README/repo
history). Without this, k3s's own `servicelb` and MetalLB both try to
control `LoadBalancer` Services and fight over the same status field.
In `/etc/rancher/k3s/config.yaml`:

```yaml
disable:
  - traefik
  - servicelb
```

then `sudo systemctl restart k3s`. This is node-level systemd
config, inherently outside what any Kubernetes manifest can express —
not something left un-declarative by choice.
