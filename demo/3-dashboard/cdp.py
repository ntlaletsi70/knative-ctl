"""Minimal Chrome DevTools Protocol client, stdlib only -- no playwright/
puppeteer/websocket-client available on this box and no way to install
them (no pip, no sudo for apt). Just enough to navigate, click via
Runtime.evaluate (simpler than simulating real mouse events), wait real
wall-clock time, and capture PNG screenshots.
"""
import base64
import hashlib
import json
import os
import socket
import struct
import subprocess
import time
import urllib.request


def _ws_connect(ws_url):
    assert ws_url.startswith("ws://")
    rest = ws_url[len("ws://"):]
    host_port, path = rest.split("/", 1)
    path = "/" + path
    host, port = (host_port.split(":") + ["80"])[:2]
    port = int(port)
    sock = socket.create_connection((host, port), timeout=10)
    key = base64.b64encode(os.urandom(16)).decode()
    req = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        f"Upgrade: websocket\r\nConnection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n"
    )
    sock.sendall(req.encode())
    resp = b""
    while b"\r\n\r\n" not in resp:
        resp += sock.recv(4096)
    return sock


def _ws_send(sock, data):
    payload = data.encode()
    n = len(payload)
    mask = os.urandom(4)
    if n <= 125:
        header = struct.pack("!BB", 0x81, 0x80 | n)
    elif n <= 65535:
        header = struct.pack("!BBH", 0x81, 0x80 | 126, n)
    else:
        header = struct.pack("!BBQ", 0x81, 0x80 | 127, n)
    masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
    sock.sendall(header + mask + masked)


def _recvn(sock, n):
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise ConnectionError("websocket closed")
        buf += chunk
    return buf


def _ws_recv(sock):
    b0, b1 = _recvn(sock, 2)
    opcode = b0 & 0x0F
    masked = b1 & 0x80
    length = b1 & 0x7F
    if length == 126:
        length = struct.unpack("!H", _recvn(sock, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", _recvn(sock, 8))[0]
    mask = _recvn(sock, 4) if masked else None
    payload = _recvn(sock, length)
    if mask:
        payload = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
    if opcode == 0x8:
        raise ConnectionError("websocket closed by peer")
    return payload.decode()


class Tab:
    def __init__(self, ws_url):
        self.sock = _ws_connect(ws_url)
        self._id = 0

    def call(self, method, params=None, timeout=25):
        self._id += 1
        my_id = self._id
        _ws_send(self.sock, json.dumps({"id": my_id, "method": method, "params": params or {}}))
        self.sock.settimeout(timeout)
        while True:
            msg = json.loads(_ws_recv(self.sock))
            if msg.get("id") == my_id:
                if "error" in msg:
                    raise RuntimeError(f"{method}: {msg['error']}")
                return msg.get("result", {})
            # ignore unsolicited events, this is a linear scripted sequence

    def navigate(self, url):
        self.call("Page.navigate", {"url": url})

    def eval(self, expression):
        return self.call("Runtime.evaluate", {"expression": expression, "returnByValue": True})

    def screenshot(self, path):
        result = self.call("Page.captureScreenshot", {"format": "png"})
        with open(path, "wb") as f:
            f.write(base64.b64decode(result["data"]))


class Chrome:
    def __init__(self, port=9333, window="1280,900"):
        self.port = port
        self.proc = subprocess.Popen(
            [
                "google-chrome", "--headless", f"--remote-debugging-port={port}",
                "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
                f"--window-size={window}", "about:blank",
            ],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        for _ in range(50):
            try:
                urllib.request.urlopen(f"http://127.0.0.1:{port}/json/version", timeout=1)
                break
            except Exception:
                time.sleep(0.2)
        else:
            raise RuntimeError("chrome devtools port never came up")

    def new_tab(self, url="about:blank"):
        req = urllib.request.Request(f"http://127.0.0.1:{self.port}/json/new?{url}", method="PUT")
        with urllib.request.urlopen(req) as r:
            info = json.loads(r.read())
        return Tab(info["webSocketDebuggerUrl"])

    def close(self):
        self.proc.terminate()
        try:
            self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.proc.kill()
