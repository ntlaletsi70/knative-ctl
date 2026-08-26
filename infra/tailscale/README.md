# External access via Tailscale

A substitute for a cloud external LB/NLB when there's no cloud budget —
`tailscaled` joins this node to an existing tailnet, and `tailscale
serve` proxies a standard HTTPS endpoint to the north-south entry
point's local NodePort. Verified end-to-end from a phone on a separate network (not
this LAN), confirming genuine north-south reachability before it hits
the same passthrough → Kourier → Revision path documented in the
top-level README.

This is a private, authenticated mesh (WireGuard, tailnet-only) — not
public internet exposure. `tailscale funnel` is the Tailscale feature
that would expose something to the raw internet; deliberately not used
here.

## Setup

```
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up                        # prints a login URL, authenticate in a browser
sudo tailscale set --operator=$USER      # one-time, avoids sudo for tailscale commands after this
tailscale serve --bg 30412               # proxies https://<node>.<tailnet>.ts.net (443) -> local nginx-ingress NodePort 80
```

`30412` is this node's current `ingress-nginx-controller` HTTP NodePort
(`kubectl get svc ingress-nginx-controller -n ingress-nginx` to
confirm/update it — `LoadBalancer` NodePorts aren't guaranteed stable
across a Service recreate). `tailscale serve` terminates TLS itself
with a real Tailscale-issued MagicDNS certificate and proxies to nginx
over plain HTTP internally — no `-k`/`--insecure` needed client-side,
unlike the LAN path's self-signed cert (`infra/ingress/tls-selfsigned.yaml`).

Check the current serve config and hostname with:

```
tailscale serve status
```

## Verified

```
curl -H "Host: knative-demo-app.knative-demo.svc.cluster.local" https://blanketops.tailf8145.ts.net/
```

run from a phone (Termux) on a different network, on the same tailnet
— returned `200` with the real Spring Boot response body.
