# ADR-0009: the cilium backend uses egressIP mode, not interface mode on the egr0 dummy

**Status:** accepted, 2026-10-21

## Context

The `cilium` backend ([ADR-0002](0002-cilium-default-host-routing-fallback.md)) rendered a
`CiliumEgressGatewayPolicy` in **interface** mode, pointed at `egr0`. `egr0` is a dummy link that only
holds the floating IP as a `/32` ([ADR-0005](0005-egr0-is-a-dummy-interface.md)); the SNAT source is the
first address on the named interface, which is the floating IP. This worked on Cilium 1.19.

On 2026-10-21, after oceanflights-dev moved to **Cilium 1.20.3**, every labelled pod lost egress: SYNs
to external hosts left the gateway node and nothing came back, with no `No Egress IP configured` drop and
no visible egress drop in the metrics. `readyGateways` was 3, the floating IPs were assigned in Hetzner
and reachable, `egr0` carried the right `/32`, `rp_filter` was 0, and the policy selected the right
nodes. On the elected gateway node, `cilium-dbg bpf egress list` showed the **correct** `Egress IP` but
`Egress Ifindex 27` — the index of **`egr0`**.

That evidence fixes the cause. The two selection modes differ in the datapath, not just in how the
source IP is chosen (Cilium docs, and cilium/cilium#49400):

- **interface** mode programs `egress_ifindex = <the named interface's index>`; the datapath sends the
  SNAT'd packet **out that interface**. Pointed at `egr0`, a dummy, that is a black hole — exactly the
  observed symptom (right egress IP, `Egress Ifindex` = `egr0`, SYN out, nothing back). Reproduced on
  kind with the same policy and an `egr0` dummy: it egresses on Cilium 1.19.9 and black-holes on 1.20.3,
  where the gateway's egress map shows the `egr0` ifindex, so 1.20 is where the datapath started
  honoring the interface. Interface-on-a-dummy only ever worked by accident.
- **egressIP** mode programs `egress_ifindex = 0`; the datapath resolves the outgoing device **per
  packet with a FIB lookup** (`ip route get <dst> from <egressIP>`), which on these nodes lands on the
  real uplink `eth0` (verified: `ip route get 1.1.1.1 from 116.203.14.77` → `via 172.31.1.1 dev eth0`).
  The floating IP still has to be a **local** address for the kernel to accept it as a source and for
  Cilium to find it — which `egr0` already provides (it is device-assigned, so cilium/cilium#49400, the
  GKE "egressIP owned only through a local route" case, does not apply here).

Two other 1.20-era issues were checked and ruled out as the cause:

- **cilium/cilium#48660** (a 1.20 regression) black-holes an IPv4 egressIP policy when `destinationCIDRs`
  carries an IPv6 entry (`::/0`) with no usable IPv6 gateway address — the gateway's BPF map shows
  `Egress IP 0.0.0.0`. Our gateway showed the *correct* egress IP, and `destinationCIDRs` is IPv4-only,
  so this is not the cause — **but it is a landmine for the fix**: the default `excludedCIDRs` injected
  IPv6 ranges (`fd00::/8`, `fe80::/10`) into an IPv4 policy.
- **cilium/cilium#44026 / #49400**: egressIP not found because it is owned only via a local route (GKE).
  Not us — the IP is device-assigned on `egr0`.

Three fix options were weighed:

1. **Add `egr0` to Cilium's `--devices`.** Rejected: a cluster-wide Cilium change for a per-node,
   post-boot dummy (needs runtime device detection; Cilium would attach NodePort/masquerade to a dummy),
   and a dummy still cannot forward — it does not fix the black hole.
2. **Switch the policy to `egressIP`** (this ADR). Datapath-correct, stays on the validated Cilium
   backend, contained to the controller.
3. **Switch to the `host-routing` backend.** Rejected as the fix and as a stopgap: the agent image is
   `distroless/static`, which has no `nft`, so host-routing SNAT silently no-ops today; it would need a
   new base image, and it is the unexercised path.

One policy, not one per IP. Rendering one policy per floating IP, all sharing the CR's single
`podSelector` and the same `destinationCIDRs`, produces multiple entries for the same
`(endpoint, destination CIDR)` key in Cilium's egress map — a genuine conflict whose resolution is
undefined and which can itself black-hole traffic (cilium/cilium#48660). Cilium has the right primitive
instead: since **1.18** a policy takes a list of gateways, **`egressGateways`**
([CFP-38341](https://github.com/cilium/design-cfps/blob/main/cilium/CFP-38341-multigateway-egress-gateway-policy.md)).
Read against the 1.20.3 source (`pkg/egressgateway/policy.go`), it does two things this controller wants:

- it **spreads**: each selected endpoint is assigned to one gateway by `fnv32a(CiliumEndpoint UID) %
  <live gateways>`, the gateways sorted by node IP, so every node computes the same assignment;
- it **fails over**: an entry whose `nodeSelector` matches no node is skipped, so its endpoints are
  re-hashed onto the remaining gateways; traffic is dropped only when no entry matches any node.

The pre-fix single policy had run one active gateway (`nbg1`, the others showing `Egress IP 0.0.0.0`), so
the platform had never used more than one of its floating IPs at a time.

## Decision

The `cilium` backend renders **one `CiliumEgressGatewayPolicy`** (named after the CR), in **`egressIP`
mode** (no `interface`), with **one `egressGateways` entry per floating IP**. Each entry's `egressIP` is
its floating IP, and its `nodeSelector` matches both the gateway label (`egress.maarlab.dev/gateway=<cr>`)
and the per-address label the agent already sets (`egress.maarlab.dev/floating-ip=<address>`), so an entry
only ever elects the node that owns its address. The CRD still requires the singular `egressGateway`; it
repeats the first entry and Cilium ignores it while the list has entries. The CRD caps the list at 64, so
the renderer refuses a CR with more floating IPs.

The default `excludedCIDRs` are **IPv4-only** (the IPv6 private ranges were dropped): the policy is a
single address family, which also sidesteps cilium/cilium#48660.

The controller applies the policy with `ForceOwnership`, so the pre-v0.2.7 interface-mode policy of the
same name upgrades in place — the stale `interface` field is dropped and the gateways installed. The agent,
the `egr0` dummy and the CRD are unchanged: `egr0` keeps holding the floating IP, now only so the kernel
accepts it as a source and Cilium's node-address lookup finds it, never as a forwarding path.

## Consequences

- Egress is restored on Cilium 1.20.x and is correct on 1.18 and 1.19. The `cilium` backend needs
  **Cilium ≥ 1.18** (`egressGateways`); interface mode on a dummy must not be reintroduced. The datapath
  e2e ([ADR-0010](0010-cilium-datapath-e2e-matrix.md)) guards the egressIP mode, the spreading and the
  failover on every supported Cilium minor and the next pre-release.
- Every floating IP is **active**: the CR's pods are spread across all of them, about evenly by count
  (not by traffic). With the platform's three regions, a pod may leave through another region's IP —
  Cilium's assignment is not topology-aware — which is why every address is allow-listed. Same-region
  egress would need one policy per region with a source `nodeSelector` and would give up cross-region
  failover; not done.
- **Failover is Cilium's**, and takes seconds once the dead gateway's labels leave its node: its pods
  move to the other floating IPs. Changing the set of live gateways re-hashes the endpoints, so it can
  move pods whose gateway did not fail and **breaks their open egress connections** (cilium/cilium#39245);
  new connections work at once. The floating IP itself moves only when its agent pod is rescheduled — for
  a dead node, once the Node object is removed — and the gateway then rejoins the set.
- Pinning is by the floating-IP **address** as a node label value, so it is **IPv4-only** (a colon in an
  IPv6 address is not a valid label value). This matches the agent's existing `floating-ip` label.
