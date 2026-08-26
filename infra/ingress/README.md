# Front door: nginx-ingress + MetalLB + Kourier

Upstream release manifests for the pattern documented in the top-level
README's "Front door" section. Applied in this order:

```
kubectl apply -f metallb-native-v0.16.0.yaml
kubectl apply -f metallb-pool.yaml
kubectl apply -f ingress-nginx-baremetal-v1.11.3.yaml
kubectl apply -f cert-manager-v1.21.1.yaml
kubectl apply -f tls-selfsigned.yaml   # after cert-manager is Ready
```

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

## Cluster-level steps not captured in any manifest here

Two changes were made directly against cluster/node state, not via
`kubectl apply` of a file in this directory:

1. **k3s's built-in `servicelb` (klipper-lb) disabled**, alongside
   Traefik (already disabled earlier in this PoC for the same reason —
   see the top-level README/repo history). Without this, k3s's own
   `servicelb` and MetalLB both try to control `LoadBalancer` Services
   and fight over the same status field. In `/etc/rancher/k3s/config.yaml`:

   ```yaml
   disable:
     - traefik
     - servicelb
   ```

   then `sudo systemctl restart k3s`.

2. **Kourier's own external `Service` reverted to `ClusterIP`**, so it
   stops competing with nginx-ingress for host ports 80/443:

   ```
   kubectl patch svc kourier -n kourier-system -p '{"spec":{"type":"ClusterIP"}}'
   ```

Both are one-time cluster setup, not something `kubectl apply -f` on
these YAMLs alone will do.
