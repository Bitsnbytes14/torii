#!/bin/sh
# Points every Torii Deployment at one image tag and waits for the rollouts.
# Runs ON the k3s node: the deploy workflow pipes it over SSH
# (`ssh node 'sh -s' -- <tag> <registry> < rollout.sh`). Keeping it a file
# rather than inline workflow YAML means it can be tested against any cluster.
#
# Usage: rollout.sh <image-tag> <registry-prefix>
#   e.g. rollout.sh 3f2c9e1... ghcr.io/bitsnbytes14
set -eu

TAG="${1:-}"
REGISTRY="${2:-}"
NAMESPACE="${NAMESPACE:-torii}"
SERVICES="booking controlplane gateway"
ROLLOUT_TIMEOUT="${ROLLOUT_TIMEOUT:-180s}"

# The tag arrives from a workflow_dispatch input, so it is validated rather
# than trusted: only a hex commit SHA can reach kubectl.
case "$TAG" in
  "" | *[!0-9a-f]*)
    echo "error: image tag must be a lowercase hex commit SHA, got '$TAG'" >&2
    exit 2
    ;;
esac
case "$REGISTRY" in
  "" | *[!a-z0-9./-]*)
    echo "error: registry prefix must be lowercase, e.g. ghcr.io/owner, got '$REGISTRY'" >&2
    exit 2
    ;;
esac

# Non-interactive SSH sessions don't source .bashrc, so fall back to the
# kubeconfig copy the node's bootstrap left in the home directory.
if [ -z "${KUBECONFIG:-}" ] && [ -f "$HOME/kubeconfig.yaml" ]; then
  export KUBECONFIG="$HOME/kubeconfig.yaml"
fi

for svc in $SERVICES; do
  kubectl -n "$NAMESPACE" set image "deployment/$svc" "$svc=$REGISTRY/torii-$svc:$TAG"
done

# With maxUnavailable: 0 a failed rollout never takes old pods down, but it
# leaves a stuck ReplicaSet retrying a bad image; undo returns the Deployment
# to its previous revision so the cluster state matches what's serving.
failed=""
for svc in $SERVICES; do
  if ! kubectl -n "$NAMESPACE" rollout status "deployment/$svc" --timeout="$ROLLOUT_TIMEOUT"; then
    echo "rollout of $svc failed; rolling back to previous revision" >&2
    kubectl -n "$NAMESPACE" rollout undo "deployment/$svc"
    failed="$failed $svc"
  fi
done

if [ -n "$failed" ]; then
  echo "error: rolled back:$failed" >&2
  exit 1
fi
echo "all deployments running $TAG"
