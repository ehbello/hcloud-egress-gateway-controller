# Runbook

Symptom first, then what to look at, then what to do. There are no status conditions to read; the
signals are `status.readyGateways` against the count you asked for, `status.floatingIPs[]`, the agent
pods, and the node labels. The controller log is in the namespace it runs in (`egress-system`); each
agent logs on its own node.

```console
kubectl get hetzneregressgateway <name> -o yaml | yq '.status'
kubectl -n egress-system get pods -o wide            # controller + egress-agent-<cr>-<region>-N
kubectl get nodes -L egress.maarlab.dev/gateway -L egress.maarlab.dev/floating-ip
```

## `readyGateways` is lower than the count you asked for

One or more regions have no live floating IP. `status.floatingIPs[].assignedNode` shows which are
placed; the missing ones are the regions to look at.

| What you find | Cause | Action |
|---|---|---|
| No agent pod for the region, or `Pending` | no `Ready` node with `topology.kubernetes.io/region=<location>` | the region has no schedulable node in that Hetzner location; add one, or drop the region from the spec |
| Agent pod `CrashLoopBackOff` | see [the agent crashes](#an-agent-pod-crashloops) below | |
| Agent `Running` but node not labelled | the agent cannot reach the Kubernetes or Hetzner API, or the token is wrong | check the agent log; `assertOnce` logs the failing step |
| Node labelled but `readyGateways` still low | the controller has not reconciled since the agent came up | wait up to 60 s, or [force a reconcile](#forcing-a-reconcile) |

## An agent pod crashloops

Read the agent log (`kubectl -n egress-system logs egress-agent-<cr>-<region>-0`). The first failing
step is logged.

| Log says | Cause | Action |
|---|---|---|
| `create dummy egr0: operation not permitted` | the agent is not running as root, so `CAP_NET_ADMIN` is not in its effective set | the chart sets `runAsUser: 0`; a `SecurityContextConstraint`/PSA policy or an override is forcing non-root. Let the agent run privileged **as root** ([ADR-0003](decisions/0003-agent-runs-as-root.md)) |
| `assign floating IP: ...` | the Hetzner token is missing, wrong, or lacks floating-IP/server write | fix the token Secret; check `spec.providerID` on the node is `hcloud://<id>` (needs the Hetzner CCM / Cluster API) |
| `ordinal N out of range for M floating IPs` | the StatefulSet has more replicas than the region has floating IPs | a stale StatefulSet; the controller reconciles it on the next pass, or force one |
| `floating IP <addr> not found` (BYO) | the address in `spec.floatingIPs` does not exist in the token's project | fix the address, or the token points at the wrong project |

## Pods opted in, but traffic does not use the floating IP

The labelled pod still egresses from its node's IP (check with an IP echo service).

- **The pod does not match `spec.podSelector`.** Compare the pod's labels with the selector; opt-in is
  by label only.
- **`cilium` backend, policy not programmed.** There is one `CiliumEgressGatewayPolicy` named after the
  CR; `kubectl get ciliumegressgatewaypolicy <cr>` should exist with one `egressGateways` entry per
  floating IP, each `egressIP` an address and its `nodeSelector` matching the single node that owns it.
  Confirm Cilium has `egressGateway.enabled: true` and is ≥ 1.18 (older versions drop `egressGateways`).
- **The destination is excluded.** Cluster-internal ranges are always excluded; if you set
  `spec.destinationCIDRs`, traffic outside them is not redirected.
- **SYN leaves the gateway node and nothing returns, no `No Egress IP configured` drop.** On the gateway
  node, `cilium-dbg bpf egress list` shows the right `Egress IP` but an `Egress Ifindex` pointing at
  `egr0`. That is Cilium's `interface` mode forwarding egress traffic *into* the dummy `egr0`, a black
  hole — it broke on Cilium 1.20 ([ADR-0009](decisions/0009-cilium-egressip-not-interface.md)). The fix
  is in the controller (≥ v0.2.7), which renders the gateways in `egressIP` mode (the datapath then does
  a per-packet FIB lookup out the real uplink), so each gateway has an `egressIP` and none an `interface`.
- **Gateway BPF map shows `Egress IP 0.0.0.0` on the elected node.** A Cilium 1.20 regression
  (cilium/cilium#48660) black-holes an IPv4 egressIP policy if any `destinationCIDRs`/`excludedCIDRs`
  entry is IPv6. The controller keeps the policy IPv4-only; if you set `spec.destinationCIDRs` or
  `spec.excludedCIDRs` by hand, keep them IPv4-only too on Cilium 1.20.x.

## Pods on a gateway node lose all cluster connectivity

Symptom: pods on a node that runs an agent cannot reach ClusterIP services or other pods, while the
node's host networking is fine. This is the failure [ADR-0004](decisions/0004-never-raise-conf-all-rp-filter.md)
exists to prevent: an agent that sets `net.ipv4.conf.all.rp_filter` to a non-zero value forces
reverse-path filtering onto the CNI's own interfaces and silently drops pod traffic on that node.

```console
# on a gateway node (via its cilium agent, hostNetwork):
cat /proc/sys/net/ipv4/conf/all/rp_filter    # must be 0; Cilium requires it
```

Current agents never touch `rp_filter`. If you see `all/rp_filter` non-zero on a gateway node, an old
agent image (≤ v0.2.4) set it: upgrade to ≥ v0.2.5, and restart the CNI agent on the affected node
(`kubectl -n kube-system rollout restart ds/cilium` or delete the node's cilium pod) to reset it to 0,
since the agent does not lower it.

## An upstream still rejects the traffic

The egress is working (the IP echo shows a floating IP) but a filtered upstream refuses it.

- **Not every address is allow-listed.** With the `cilium` backend the CR's pods are spread across all
  of its floating IPs, and a pod's address can change whenever a gateway fails or returns
  ([operations](operations.md#regions-and-locality)). Allow-list every address in
  `status.floatingIPs[]`, not just one.
- **The address changed.** Managed IPs are stable and re-adopted by label, so this only happens if the
  CR was deleted with `reclaimPolicy: Delete` and recreated, minting new addresses. Use `Retain`.

## A node kept its gateway labels after being drained

A node labelled `egress.maarlab.dev/gateway=<cr>` with no agent pod on it, or that is not `Ready`, is
cleaned up by the controller on its next pass (it removes the `gateway` and `floating-ip` labels), and
Cilium moves that gateway's pods to the others. If it lingers, the controller is not reconciling —
check the controller pod is `Running` and is the leader, and [force a reconcile](#forcing-a-reconcile).

## Forcing a reconcile

The controller reconciles each CR on spec changes, on changes to nodes it is a gateway for, and every
60 s. To check everything now, restart it:

```console
kubectl -n egress-system rollout restart deployment hcloud-egress-gateway
```

To force an agent to re-assert its node (re-assign the floating IP, recreate `egr0`, relabel), delete
its pod; the StatefulSet recreates it and `assertOnce` runs on start.
