#!/usr/bin/env bash
# Scripted demo run for the bottom pane of demo/screenrc. Not meant to be
# run standalone outside that layout (it calls `screen -X quit` at the end
# to stop the whole recording session, k9s pane included).
set -euo pipefail

CTL=/home/blanketops/knative-ctl/knative-ctl
NS=knative-demo
SVC=knative-demo-app
GO_IMAGE_V2="gcr.io/knative-samples/helloworld-go@sha256:da76ee72d7f2e251267af3b6e53e2a1325c385b272b790b7fcbd1363ee1cb482"
GO_IMAGE_V3="gcr.io/knative-samples/helloworld-go@sha256:b9452976281b790fd56cbae50e5e22003a9bd0a296425fe42256e78a2dd96e2e"

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
for i in 1 2 3 4 5; do
  kubectl get ksvc "$SVC" -n "$NS" >/dev/null 2>&1 && break
  sleep 2
done

step "knative-ctl demo: canary release"
"$CTL" canary "$SVC" "$GO_IMAGE_V2" --namespace "$NS" --steps 10,50,100 --interval 8s

step "canary complete -- current traffic split"
kubectl get ksvc "$SVC" -n "$NS" -o jsonpath='{.status.traffic}' | python3 -m json.tool
sleep 4

step "knative-ctl demo: blue-green release"
"$CTL" bluegreen "$SVC" "$GO_IMAGE_V3" --namespace "$NS"

step "blue-green complete -- current traffic split"
kubectl get ksvc "$SVC" -n "$NS" -o jsonpath='{.status.traffic}' | python3 -m json.tool
sleep 4

step "demo done -- watch the top pane: this revision scales to zero after ~90s idle"
sleep 6

screen -X quit
