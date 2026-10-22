# ADR-0002: Cilium egress gateway as the default backend, host-routing as the CNI-independent fallback

**Status:** accepted, 2026-10-14

## Context

The floating-IP lifecycle, node assignment and interface handling are CNI-independent; only the last
step — redirecting the selected pods' egress through the gateway nodes and SNAT'ing to the floating IP
— depends on the CNI ([ADR-0001](0001-floating-ip-controller-not-datapath.md)). The cluster runs
Cilium with its egress gateway enabled, but the project is meant to be reusable where that is not the
case.

## Decision

A pluggable backend, `spec.backend`, with the redirect behind a `Renderer` interface:

- **`cilium`** (default): the controller renders one `CiliumEgressGatewayPolicy` selecting
  `spec.podSelector`, with `destinationCIDRs` defaulting to `0.0.0.0/0` minus the cluster-internal
  ranges, and one `egressGateways` entry per floating IP in **`egressIP` mode**, each pinned to the node
  that owns its address. Cilium does the redirect, the SNAT, the spreading of pods across the gateways
  and the failover between them. (This started in `interface` mode on
  `egr0`; that broke on Cilium 1.20 and was replaced — [ADR-0009](0009-cilium-egressip-not-interface.md).)
- **`host-routing`**: the controller renders nothing cluster-level; the agent programs policy routing
  and an `nft` SNAT rule to the floating IP on the node's default-route uplink, in the host netns.
  Works on any CNI that permits host-level SNAT; spreading and failover are whatever the routing gives.

Only the `cilium` backend is the exercised path on this platform. `host-routing` is a best-effort
extension point and needs `nft` in the agent image.

## Consequences

- Adding support for another CNI's egress construct is implementing one `Renderer`; the rest of the
  controller and the agent are reused unchanged.
- The default path depends on Cilium ≥ 1.18 (`egressGateways`; validated on 1.18–1.20.x by the
  datapath e2e, [ADR-0010](0010-cilium-datapath-e2e-matrix.md)) with `egressGateway.enabled: true`, and
  inherits its OSS limitation of not being topology-aware.
- `cilium` uses Cilium's `egressIP` mode ([ADR-0009](0009-cilium-egressip-not-interface.md)); the
  floating IP still lives on `egr0` ([ADR-0005](0005-egr0-is-a-dummy-interface.md)) as a local source
  address, so `spec.egressInterface` (default `egr0`) still names the agent's interface even though the
  policy no longer references it.
