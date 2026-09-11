#!/usr/bin/env bash
# Copies the kube-state-metrics and kubelet ServiceMonitors (wherever
# kube-prometheus-stack actually put them) into the current kubectl
# context's namespace, so a PrometheusInstance living there can discover
# them despite internal/prometheus's namespace-scoped selectors. See the
# "Prometheus Operator" section of the README for background.
set -euo pipefail

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

TARGET_NS=$(kubectl config view --minify -o jsonpath='{..namespace}')
TARGET_NS=${TARGET_NS:-default}

find_servicemonitor() {
  kubectl get servicemonitor -A -o json | jq -r --arg pattern "$1" '
    .items[] | select(.metadata.name | test($pattern))
    | "\(.metadata.namespace)/\(.metadata.name)"
  ' | head -n1
}

KSM_SM=$(find_servicemonitor 'kube-state-metrics')
KUBELET_SM=$(find_servicemonitor 'kubelet')

for src in "$KSM_SM" "$KUBELET_SM"; do
  if [[ -z "$src" ]]; then
    echo "warning: no matching ServiceMonitor found, skipping" >&2
    continue
  fi

  SRC_NS=${src%%/*}
  SRC_NAME=${src#*/}

  if [[ "$SRC_NS" == "$TARGET_NS" ]]; then
    echo "skipping $src: already in $TARGET_NS"
    continue
  fi

  kubectl get servicemonitor -n "$SRC_NS" "$SRC_NAME" -o json | jq \
    --arg ns "$TARGET_NS" --arg srcns "$SRC_NS" '
      .metadata.namespace = $ns
      | .metadata.name += "-copy"
      | del(.metadata.uid, .metadata.resourceVersion, .metadata.creationTimestamp,
            .metadata.generation, .metadata.ownerReferences, .metadata.selfLink, .status)
      | .spec.namespaceSelector = {"matchNames": [$srcns]}
    ' | kubectl apply -f -
done
