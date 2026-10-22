#!/usr/bin/env bash
# Tear down what hack/e2e/up.sh created.
set -euo pipefail
CLUSTER=${E2E_CLUSTER:-heg-e2e}
kind delete cluster --name "$CLUSTER"
docker rm -f "$CLUSTER-echo" >/dev/null 2>&1 || true
docker network rm "$CLUSTER" >/dev/null 2>&1 || true
