#!/usr/bin/env bash
# Scripted demo run for the bottom pane of demo/2-canary-bluegreen/screenrc.
# Not meant to be run standalone outside that layout (it calls
# `screen -X quit` at the end to stop the whole recording session, k9s
# pane included).
set -euo pipefail

CTL=/home/blanketops/knative-ctl/knative-ctl
NS=knative-demo
SVC=knative-demo-app
# Same image reused for both steps -- knative-ctl always assigns a fresh
# revision name regardless of image identity, so this still exercises the
# real traffic-splitting mechanics. Reusing it (rather than building a
# distinct v2) also means the image is already cached on this node from
# the initial deploy, keeping each step's cold start to JVM boot time only
# (no re-pull).
APP_IMAGE="ghcr.io/ntlaletsi70/knative-demo-app:latest"

step() {
  echo
  echo "### $1"
  echo
  sleep 2
}

# Guard against a transient apiserver blip right at the start (seen once
# after heavy concurrent cluster load): retry the initial lookup a few
# times before handing off to knative-ctl, which has no retry of its own
# for this call.
echo "============================================================"
echo " Demo 2: canary and blue-green release flows"
echo
echo " What you're about to see: knative-ctl driving two release"
echo " strategies against the '${SVC}' Knative Service, both built"
echo " directly on spec.traffic revision splitting -- no Argo"
echo " Rollouts, no Flagger, no service mesh. First a progressive"
echo " canary (10% -> 50% -> 100%), then a blue-green dark-deploy and"
echo " atomic cutover. Watch the k9s pane above for the new revision's"
echo " pod appearing before traffic ever reaches it."
echo "============================================================"
sleep 3

for i in 1 2 3 4 5; do
  kubectl get ksvc "$SVC" -n "$NS" >/dev/null 2>&1 && break
  sleep 2
done

step "knative-ctl demo: canary release"
"$CTL" canary "$SVC" "$APP_IMAGE" --namespace "$NS" --steps 10,50,100 --interval 8s

step "canary complete -- current traffic split"
kubectl get ksvc "$SVC" -n "$NS" -o jsonpath='{.status.traffic}' | python3 -m json.tool
sleep 4

step "knative-ctl demo: blue-green release"
"$CTL" bluegreen "$SVC" "$APP_IMAGE" --namespace "$NS"

step "blue-green complete -- current traffic split"
kubectl get ksvc "$SVC" -n "$NS" -o jsonpath='{.status.traffic}' | python3 -m json.tool
sleep 4

step "demo done -- watch the top pane: this revision scales to zero after ~90s idle"
sleep 6

screen -X quit
