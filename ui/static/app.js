// -- Release form --------------------------------------------------------

const actionEl = document.getElementById("action");
const imageField = document.querySelector(".field-image");
const revisionField = document.querySelector(".field-revision");
const stepsFields = document.querySelectorAll(".field-steps");
const form = document.getElementById("release-form");
const runBtn = document.getElementById("run-btn");
const logEl = document.getElementById("release-log");
const serviceInput = document.getElementById("service");
const releaseNsInput = document.getElementById("namespace");
const imageOptions = document.getElementById("image-options");
const revisionOptions = document.getElementById("revision-options");

function syncFieldsToAction() {
  const action = actionEl.value;
  imageField.hidden = action === "rollback";
  revisionField.hidden = action !== "rollback";
  stepsFields.forEach((f) => (f.hidden = action !== "canary"));
}
actionEl.addEventListener("change", syncFieldsToAction);
syncFieldsToAction();

// Past revisions' images/names are exactly what's actually deployable --
// already built, already pulled -- more useful to pick from than typing
// a tag from memory. <datalist> keeps the field free-text too, for a
// brand new image that hasn't been deployed yet.
async function refreshRevisions() {
  const service = serviceInput.value.trim();
  const namespace = releaseNsInput.value.trim() || "knative-demo";
  if (!service) return;
  try {
    const res = await fetch(
      "/api/revisions?service=" + encodeURIComponent(service) +
      "&namespace=" + encodeURIComponent(namespace)
    );
    if (!res.ok) return;
    const revs = await res.json();
    imageOptions.innerHTML = "";
    revisionOptions.innerHTML = "";
    const seenImages = new Set();
    for (const rev of revs) {
      if (rev.image && !seenImages.has(rev.image)) {
        seenImages.add(rev.image);
        const opt = document.createElement("option");
        opt.value = rev.image;
        imageOptions.appendChild(opt);
      }
      const opt = document.createElement("option");
      opt.value = rev.name;
      opt.label = rev.ready ? "ready" : "not ready";
      revisionOptions.appendChild(opt);
    }
  } catch {
    // best-effort -- the fields stay free-text either way
  }
}
serviceInput.addEventListener("change", refreshRevisions);
releaseNsInput.addEventListener("change", refreshRevisions);
refreshRevisions();

let releaseSource = null;

form.addEventListener("submit", (ev) => {
  ev.preventDefault();
  if (releaseSource) releaseSource.close();

  const params = new URLSearchParams();
  const data = new FormData(form);
  for (const [k, v] of data.entries()) {
    if (v !== "") params.set(k, v);
  }

  logEl.textContent = "";
  runBtn.disabled = true;

  releaseSource = new EventSource("/api/release/stream?" + params.toString());
  releaseSource.onmessage = (ev) => {
    logEl.textContent += ev.data + "\n";
    logEl.scrollTop = logEl.scrollHeight;
  };
  releaseSource.addEventListener("done", (ev) => {
    logEl.textContent += "\n-- " + ev.data + " --\n";
    logEl.scrollTop = logEl.scrollHeight;
    runBtn.disabled = false;
    releaseSource.close();
  });
  releaseSource.addEventListener("error", (ev) => {
    if (ev.data) logEl.textContent += "\nerror: " + ev.data + "\n";
    runBtn.disabled = false;
    releaseSource.close();
  });
});

// -- Live pods ------------------------------------------------------------

const podsGrid = document.getElementById("pods-grid");
const nsInput = document.getElementById("namespace");
const nsLabel = document.getElementById("pods-namespace-label");
let podsSource = null;
let known = new Map(); // name -> card element

function connectPods(namespace) {
  if (podsSource) podsSource.close();
  nsLabel.textContent = "(" + namespace + ")";
  podsSource = new EventSource(
    "/api/pods/stream?namespace=" + encodeURIComponent(namespace)
  );
  podsSource.onmessage = (ev) => {
    let pods;
    try {
      pods = JSON.parse(ev.data);
    } catch {
      return;
    }
    renderPods(pods);
  };
}

function renderPods(pods) {
  const seen = new Set();

  if (pods.length === 0 && known.size === 0) {
    podsGrid.innerHTML = '<p class="empty-state">no pods -- scaled to zero</p>';
    return;
  }
  const emptyState = podsGrid.querySelector(".empty-state");
  if (emptyState) emptyState.remove();

  for (const pod of pods) {
    seen.add(pod.name);
    let card = known.get(pod.name);
    if (!card) {
      card = document.createElement("div");
      card.className = "pod-card pod-enter";
      podsGrid.appendChild(card);
      known.set(pod.name, card);
      requestAnimationFrame(() => card.classList.remove("pod-enter"));
    }
    card.innerHTML = `
      <div class="pod-name">${pod.name}</div>
      <div class="pod-row"><span>service</span><span>${pod.service || "-"}</span></div>
      <div class="pod-row"><span>revision</span><span>${pod.revision || "-"}</span></div>
      <div class="pod-row"><span>phase</span><span class="phase-${pod.phase}">${pod.phase}</span></div>
      <div class="pod-row"><span>ready</span><span>${pod.ready}</span></div>
      <div class="pod-row"><span>restarts</span><span>${pod.restarts}</span></div>
      <div class="pod-row"><span>age</span><span>${formatAge(pod.ageSeconds)}</span></div>
    `;
  }

  for (const [name, card] of known) {
    if (!seen.has(name)) {
      card.classList.add("pod-exit");
      setTimeout(() => card.remove(), 400);
      known.delete(name);
    }
  }
}

function formatAge(secs) {
  if (secs < 60) return secs + "s";
  if (secs < 3600) return Math.floor(secs / 60) + "m";
  return Math.floor(secs / 3600) + "h";
}

nsInput.addEventListener("change", () => connectPods(nsInput.value || "knative-demo"));
connectPods(nsInput.value || "knative-demo");

// -- Traffic spike ---------------------------------------------------------

const loadtestForm = document.getElementById("loadtest-form");
const loadtestBtn = document.getElementById("loadtest-btn");
const loadtestLog = document.getElementById("loadtest-log");
let loadtestSource = null;

loadtestForm.addEventListener("submit", (ev) => {
  ev.preventDefault();
  if (loadtestSource) loadtestSource.close();

  const params = new URLSearchParams(new FormData(loadtestForm));
  loadtestLog.textContent = "";
  loadtestBtn.disabled = true;

  loadtestSource = new EventSource("/api/loadtest/stream?" + params.toString());
  loadtestSource.onmessage = (ev) => {
    loadtestLog.textContent += ev.data + "\n";
    loadtestLog.scrollTop = loadtestLog.scrollHeight;
  };
  loadtestSource.addEventListener("done", (ev) => {
    loadtestLog.textContent += "\n-- done: " + ev.data + " --\n";
    loadtestBtn.disabled = false;
    loadtestSource.close();
  });
  loadtestSource.onerror = () => {
    loadtestBtn.disabled = false;
    loadtestSource.close();
  };
});

// -- Live traffic ----------------------------------------------------------

const trafficStatus = document.getElementById("traffic-status");
const trafficWindow = document.getElementById("traffic-window");
const trafficRevisions = document.getElementById("traffic-revisions");
const trafficTable = document.getElementById("traffic-table");
const trafficRows = document.getElementById("traffic-rows");

// Everything in a snapshot originates from request data (URLs, header
// values), so it goes through here before it's put into markup.
function esc(value) {
  return String(value).replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

function formatMs(ms) {
  if (ms >= 1000) return (ms / 1000).toFixed(2) + "s";
  return (ms >= 10 ? ms.toFixed(0) : ms.toFixed(1)) + "ms";
}

function renderTraffic(snap) {
  trafficWindow.textContent =
    "(last " + snap.windowSeconds + "s" + (snap.sampled ? ", sampled" : "") + ")";

  if (snap.total === 0) {
    trafficStatus.hidden = false;
    trafficStatus.textContent = "no requests in the last " + snap.windowSeconds + "s";
  } else {
    trafficStatus.hidden = true;
  }

  // Grouped by service, one bar per revision: during a canary this is
  // the traffic split as requests actually landed, not as configured.
  const byService = new Map();
  for (const rev of snap.revisions) {
    if (!byService.has(rev.service)) byService.set(rev.service, []);
    byService.get(rev.service).push(rev);
  }
  let html = "";
  for (const [service, revs] of byService) {
    html += `<div class="traffic-service"><div class="traffic-service-name">${esc(service)}</div>`;
    for (const rev of revs) {
      const pct = Math.round(rev.share * 100);
      html += `
        <div class="traffic-rev">
          <span class="traffic-rev-name">${esc(rev.revision)}</span>
          <span class="traffic-bar"><span class="traffic-bar-fill" style="width:${pct}%"></span></span>
          <span class="traffic-rev-stat">${pct}%</span>
          <span class="traffic-rev-stat">${rev.count} req</span>
          <span class="traffic-rev-stat">avg ${formatMs(rev.avgMs)}</span>
          <span class="traffic-rev-stat">max ${formatMs(rev.maxMs)}</span>
          <span class="traffic-rev-stat ${rev.errors ? "traffic-error" : ""}">${rev.errors} err</span>
        </div>`;
    }
    html += "</div>";
  }
  trafficRevisions.innerHTML = html;

  trafficTable.hidden = snap.traces.length === 0;
  trafficRows.innerHTML = snap.traces.map((t) => {
    const time = new Date(t.timestamp).toLocaleTimeString();
    const badges =
      (t.event ? `<span class="badge">${esc(t.event)}</span>` : "") +
      (t.coldStartMs ? `<span class="badge badge-cold">cold start ${formatMs(t.coldStartMs)}</span>` : "");
    return `
      <tr class="${t.error ? "traffic-error" : ""}">
        <td>${esc(time)}</td>
        <td><a href="/zipkin/traces/${encodeURIComponent(t.id)}" target="_blank" rel="noopener">${esc(t.request)}</a>${badges}</td>
        <td class="traffic-hops">${t.hops.map(esc).join(" → ")}</td>
        <td class="num">${esc(t.status || "-")}</td>
        <td class="num">${formatMs(t.durationMs)}</td>
      </tr>`;
  }).join("");
}

const trafficSource = new EventSource("/api/traffic/stream");
trafficSource.onmessage = (ev) => {
  try {
    renderTraffic(JSON.parse(ev.data));
  } catch {
    // skip a malformed snapshot, the next one is two seconds away
  }
};
trafficSource.addEventListener("unavailable", (ev) => {
  trafficStatus.hidden = false;
  trafficStatus.textContent = ev.data;
  trafficRevisions.innerHTML = "";
  trafficTable.hidden = true;
});
