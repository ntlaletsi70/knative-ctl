#!/usr/bin/env bash
# Scripted demo run for the bottom pane of demo/1-traffic-spike/screenrc.
# Not meant to be run standalone outside that layout (it calls
# `screen -X quit` at the end to stop the whole recording session, k9s
# pane included).
set -euo pipefail

NS=knative-demo
SVC=knative-demo-app
HOST="${SVC}.${NS}.svc.cluster.local"
LOCAL_PORT=8092
WORKERS=15
BURST_SECONDS=25

step() {
  echo
  echo "### $1"
  echo
  sleep 2
}

pods() {
  # `|| true` guards against set -e + pipefail treating a transient kubectl
  # hiccup as fatal when this is used in a bare `n=$(pods)` assignment.
  kubectl get pods -n "$NS" -l "serving.knative.dev/service=$SVC" --no-headers 2>/dev/null | wc -l || true
}

for i in 1 2 3 4 5; do
  kubectl get ksvc "$SVC" -n "$NS" >/dev/null 2>&1 && break
  sleep 2
done

step "resetting to a single revision at 100% traffic (clean baseline)"
kubectl patch ksvc "$SVC" -n "$NS" --type=merge -p '{"spec":{"traffic":[{"latestRevision":true,"percent":100}]}}'
sleep 3

step "waiting for a clean idle baseline (scale-to-zero, up to ~30s)"
for i in $(seq 1 6); do
  n=$(pods)
  echo "pods running: $n"
  if [ "$n" -eq 0 ]; then
    break
  fi
  sleep 5
done

step "starting port-forward to kourier-internal"
kubectl port-forward -n kourier-system svc/kourier-internal "${LOCAL_PORT}:80" >/tmp/demo1-pf.log 2>&1 &
PF_PID=$!
sleep 3

step "firing ${WORKERS} concurrent workers for ${BURST_SECONDS}s -- watch the top pane scale out"
WORKER_PIDS=()
for i in $(seq 1 "$WORKERS"); do
  ( timeout "$BURST_SECONDS" bash -c "while true; do curl -s -o /dev/null -H 'Host: ${HOST}' http://localhost:${LOCAL_PORT}; done" ) &
  WORKER_PIDS+=("$!")
done

for i in 1 2 3 4 5; do
  sleep 5
  echo "t+$((i*5))s: pods running: $(pods)"
done
wait "${WORKER_PIDS[@]}" 2>/dev/null || true

kill "$PF_PID" 2>/dev/null || true

step "load stopped -- watching it scale back down to zero (up to ~90s)"
for i in $(seq 1 9); do
  sleep 10
  n=$(pods)
  echo "t+$((i*10))s: pods running: $n"
  if [ "$n" -eq 0 ]; then
    break
  fi
done

step "demo done"
sleep 4

screen -X quit
