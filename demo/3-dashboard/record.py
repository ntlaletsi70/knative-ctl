"""Drives the dashboard via CDP (see cdp.py) to record a demo: a canary
release, then a traffic-spike simulation watched live in the Pods panel.
Screenshots assembled into demo.gif by gif.py.

The canary is triggered ON the local dashboard (http://127.0.0.1:8090)
via its own form. The traffic spike is fired externally, straight at the
in-cluster instance through the front door -- kourier-internal isn't
reachable from outside the cluster, so the LOCAL dashboard can't fire it
itself (see ui/README.md). The local dashboard's Pods panel still shows
it happening live either way, since it reads the same cluster via
kubectl regardless of which instance is doing the polling.
"""
import json
import subprocess
import time

from cdp import Chrome


def demo_app_pod_count():
    out = subprocess.run(
        ["kubectl", "get", "pod", "-n", "knative-demo",
         "-l", "serving.knative.dev/service=knative-demo-app", "--no-headers"],
        capture_output=True, text=True,
    ).stdout.strip()
    return 0 if not out else len(out.splitlines())

FRAMES_DIR = "frames"
IMAGE = "ttl.sh/knative-demo-app-58f4d5eb247e633e2e40dfaf1cb809d7e832a141:24h"
FRONT_DOOR_HOST = "knative-ui.knative-demo.svc.cluster.local"
FRONT_DOOR_IP = "192.168.0.200"


def shot(tab, n, name):
    path = f"{FRAMES_DIR}/{n:03d}-{name}.png"
    try:
        tab.screenshot(path)
        print("captured", path)
        return n + 1
    except Exception as e:
        print(f"skipped {path}: {e}")
        return n


def main():
    subprocess.run(["mkdir", "-p", FRAMES_DIR], check=True)

    chrome = Chrome()
    try:
        tab = chrome.new_tab("http://127.0.0.1:8090/")
        time.sleep(1.5)

        n = 0
        n = shot(tab, n, "idle")

        print("-- filling and submitting canary form --")
        tab.eval(f"""
            document.getElementById('image').value = {json.dumps(IMAGE)};
            document.getElementById('steps').value = '50,100';
            document.getElementById('interval').value = '5s';
            document.getElementById('release-form').requestSubmit();
        """)
        time.sleep(1)
        n = shot(tab, n, "canary-start")

        for _ in range(20):
            time.sleep(3)
            n = shot(tab, n, "canary-progress")
            try:
                r = tab.eval("document.getElementById('release-log').textContent")
                text = r.get("result", {}).get("value", "")
            except Exception:
                text = ""
            if "-- ok --" in text or "-- error" in text:
                break
        n = shot(tab, n, "canary-done")

        print("-- cooling down, waiting for scale-to-zero before the spike --")
        cool_deadline = time.time() + 100
        while time.time() < cool_deadline:
            time.sleep(12)
            n = shot(tab, n, "cooldown")
            if demo_app_pod_count() == 0:
                print("scaled to zero, moving on")
                break

        print("-- firing traffic spike externally against the in-cluster instance --")
        curl = subprocess.Popen(
            [
                "curl", "-sSk", "-N",
                "-H", f"Host: {FRONT_DOOR_HOST}",
                f"https://{FRONT_DOOR_IP}/api/loadtest/stream"
                "?service=knative-demo-app&namespace=knative-demo&workers=10&duration=25",
            ],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )

        # Also reflect the loadtest's own progress text in the local
        # dashboard's log pane, so the GIF shows *something* narrating the
        # spike even though this instance didn't trigger it itself.
        try:
            tab.eval("""
                document.getElementById('loadtest-log').textContent =
                  'firing 10 workers at knative-demo-app for 25s (triggered against the in-cluster instance) ...';
            """)
        except Exception:
            pass
        n = shot(tab, n, "spike-start")

        deadline = time.time() + 27
        while time.time() < deadline:
            time.sleep(3)
            n = shot(tab, n, "spike-progress")

        curl.wait(timeout=10)
        n = shot(tab, n, "spike-done")

        print(f"done, {n} frames in {FRAMES_DIR}/")
    finally:
        chrome.close()


if __name__ == "__main__":
    main()
