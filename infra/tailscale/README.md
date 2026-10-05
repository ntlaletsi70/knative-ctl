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

## The dashboard in a browser

A browser can't set a `Host` header the way the `curl` below does, so
the dashboard gets its own route: `dashboard-ingress.yaml` matches this
node's tailnet hostname on nginx-ingress and rewrites it to the
dashboard's cluster-local name. Set its `host` to your own MagicDNS
name, then:

```
kubectl apply -f infra/tailscale/dashboard-ingress.yaml
sudo tailscale serve --bg --https=8443 http://<nginx-ingress address>
# https://<node>.<tailnet>.ts.net:8443
```

`<nginx-ingress address>` is `127.0.0.1:<HTTP NodePort>` on a node that
runs Kubernetes directly. On `kind` the node is a container, so
NodePorts aren't on the host's loopback — use the node container's IP
instead:

```
docker inspect <cluster>-control-plane --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
kubectl get svc ingress-nginx-controller -n ingress-nginx -o jsonpath='{.spec.ports[?(@.port==80)].nodePort}'
```

Use `serve`, never `funnel`, for this: the dashboard has no
authentication and can trigger releases. A separate port (`8443`) also
keeps it clear of anything already published on `443`.

## Verified

```
curl -H "Host: knative-demo-app.knative-demo.svc.cluster.local" https://blanketops.tailf8145.ts.net/
```

run from a phone (Termux) on a different network, on the same tailnet
— returned `200` with the real Spring Boot response body.
