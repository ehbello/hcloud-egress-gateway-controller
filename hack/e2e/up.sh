#!/usr/bin/env bash
# Bring up the environment for the Cilium datapath e2e (test/e2e):
#
#   - a kind cluster on its own docker network, running Cilium $CILIUM_VERSION with the
#     egress gateway enabled;
#   - an "upstream" outside the cluster: an agnhost echo server that reports the source IP
#     it sees;
#   - three simulated gateway nodes, doing what the agent does on Hetzner minus the Hetzner
#     API: an egr0 dummy holding a "floating IP" /32, and the route back to it, which on
#     Hetzner is the floating-IP assignment and here is a route on the upstream.
#
# The Go test then labels the nodes, renders and applies the policy with the controller's
# own backend, and asserts on the source IPs the upstream sees.
#
# The network is 198.18.0.0/24 (RFC 2544, benchmarking) on purpose: kind's default network
# is in 172.16.0.0/12, which the controller always excludes from the egress path, so with it
# the default policy (0.0.0.0/0 minus the private ranges) could not be tested. The "floating
# IPs" are TEST-NET-3 (203.0.113.0/24). Keep these values in step with test/e2e.
set -euo pipefail

CILIUM_VERSION=${CILIUM_VERSION:?set CILIUM_VERSION, e.g. 1.20.3}
CLUSTER=${E2E_CLUSTER:-heg-e2e}
NODE_IMAGE=${E2E_NODE_IMAGE:-kindest/node:v1.36.4}
SUBNET=198.18.0.0/24
ECHO_IP=198.18.0.200
AGNHOST=registry.k8s.io/e2e-test-images/agnhost:2.61
ALPINE=alpine:3.22
here=$(cd "$(dirname "$0")" && pwd)

docker network inspect "$CLUSTER" >/dev/null 2>&1 || docker network create --subnet "$SUBNET" "$CLUSTER"
KIND_EXPERIMENTAL_DOCKER_NETWORK=$CLUSTER \
  kind create cluster --name "$CLUSTER" --image "$NODE_IMAGE" --config "$here/kind.yaml"

# --devel lets a pre-release (1.21.0-pre.N) resolve; it is a no-op for a stable version.
helm upgrade --install cilium cilium --repo https://helm.cilium.io --devel \
  --version "$CILIUM_VERSION" --namespace kube-system \
  --set kubeProxyReplacement=true \
  --set k8sServiceHost="$CLUSTER-control-plane" --set k8sServicePort=6443 \
  --set egressGateway.enabled=true --set bpf.masquerade=true \
  --set ipam.mode=kubernetes --set operator.replicas=1 \
  --wait --timeout 10m
kubectl wait --for=condition=Ready nodes --all --timeout=5m
kubectl wait --for=condition=established crd/ciliumegressgatewaypolicies.cilium.io --timeout=2m

docker rm -f "$CLUSTER-echo" >/dev/null 2>&1 || true
docker run -d --name "$CLUSTER-echo" --network "$CLUSTER" --ip "$ECHO_IP" \
  "$AGNHOST" netexec --http-port=8080 >/dev/null

i=10
for node in "$CLUSTER-worker" "$CLUSTER-worker2" "$CLUSTER-worker3"; do
  fip=203.0.113.$i
  docker exec "$node" sh -c "ip link add egr0 type dummy && ip link set egr0 up && ip addr add $fip/32 dev egr0"
  nodeip=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$CLUSTER\").IPAddress}}" "$node")
  docker run --rm --network "container:$CLUSTER-echo" --cap-add NET_ADMIN "$ALPINE" \
    ip route add "$fip/32" via "$nodeip"
  i=$((i + 1))
done
echo "e2e environment up: Cilium $CILIUM_VERSION, upstream http://$ECHO_IP:8080"
