# Demo 3: dashboard

A canary release triggered from the UI, then a traffic-spike simulation
watched live in the Pods panel — a real 0→2 pod scale-out.

Recorded differently from demos 1/2: this is a browser page, not a
terminal, so `asciinema`/`screen` doesn't apply. No Playwright/Puppeteer
available on this box (no `pip`, no `sudo` for `apt`), so `cdp.py` is a
minimal hand-rolled Chrome DevTools Protocol client (stdlib only —
socket/base64/hashlib/struct for the WebSocket handshake and framing)
that navigates, submits the release form via `Runtime.evaluate`
(simpler than simulating real mouse events), and captures screenshots
via `Page.captureScreenshot`. `--headless=new` mode captured stale
frames (DOM had live data, screenshot didn't reflect it) — plain
`--headless` doesn't have that problem.

`record.py` drives the sequence:

- Canary against `knative-demo-app`, triggered on the local dashboard
  (`http://127.0.0.1:8090`, must be running: `go build -o knative-ui
  ./ui && ./knative-ui` from the repo root).
- ~90s cool-down, polling pod count, so the traffic spike starts from a
  genuine scale-to-zero rather than an already-warm pod (the first take
  skipped this and the spike showed no visible scaling as a result).
- The spike itself fires externally, straight at the **in-cluster**
  `knative-ui` instance through the front door — `kourier-internal`
  isn't reachable from outside the cluster, so the local dashboard can't
  trigger it itself (see `../../ui/README.md`). The local dashboard's
  Pods panel still shows it live either way, since it reads the same
  cluster via `kubectl` regardless of which instance is doing the
  polling.

Re-run it yourself (needs the local dashboard running, the in-cluster
one deployed, and `google-chrome` installed):

```
python3 record.py
python3 gif.py
```
