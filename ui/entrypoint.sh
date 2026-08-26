#!/bin/sh
# knative-ui shells out to `kubectl`, same as knative-ctl itself -- but
# inside a pod there's no kubeconfig file, just the mounted ServiceAccount
# token/CA at /var/run/secrets/kubernetes.io/serviceaccount/. Build a
# kubeconfig from that once at startup (the standard, well-known pattern
# for using the plain kubectl CLI, as opposed to client-go, from inside a
# pod) rather than hand-rolling --server/--token flags on every call.
set -e

NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace 2>/dev/null || echo default)

kubectl config set-cluster in-cluster \
  --server=https://kubernetes.default.svc \
  --certificate-authority=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt >/dev/null
kubectl config set-credentials in-cluster \
  --token="$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" >/dev/null
kubectl config set-context in-cluster \
  --cluster=in-cluster --user=in-cluster --namespace="$NAMESPACE" >/dev/null
kubectl config use-context in-cluster >/dev/null

exec /usr/local/bin/knative-ui --addr=0.0.0.0:8090 --knative-ctl=/usr/local/bin/knative-ctl
