# ADR-0004: The agent never raises net.ipv4.conf.all.rp_filter

**Status:** accepted, 2026-10-14

## Context

An early agent set `net.ipv4.conf.all.rp_filter = 2` (and the same on `egr0`), the stated intent being
"loose reverse-path filtering so asymmetric egress return traffic isn't dropped". After the agent was
fixed to run as root ([ADR-0003](0003-agent-runs-as-root.md)) and actually reached that line, every
node running an agent lost pod networking: pods on gateway nodes could not reach ClusterIP services or
other pods, while the node's host netns was fine. It was mistaken for node churn and triggered a
cluster-wide outage.

The cause, confirmed on-cluster: the **effective** reverse-path filter of an interface is
`max(conf.all.rp_filter, conf.<iface>.rp_filter)`. Cilium requires `conf.all.rp_filter = 0` and keeps
its own interfaces (`cilium_host`, the `lxc*` pod veths) at 0. Setting `conf.all` to a non-zero value
forces RPF back on over all of them and silently drops the CNI's asymmetrically-routed pod traffic. The
correlation was exact: every node with an agent had `all/rp_filter = 2` and a broken datapath; the one
worker without an agent had `0` and worked.

## Decision

The agent does **not** touch `rp_filter` at all — not `conf.all`, not the egress interface. The
`writeSysctl` helper was removed. `egr0` is a dummy that only holds the floating IP; nothing ingresses
it, so no reverse-path tuning is needed for egress SNAT to work. A comment at the call site records why
raising `conf.all.rp_filter` is forbidden.

## Consequences

- Gateway nodes keep the CNI's required `conf.all.rp_filter = 0`; pod networking on them is unaffected
  by the agent.
- The fix only stops *new* breakage. A node an old agent already set to non-zero is not lowered by the
  agent; it needs the CNI agent restarted (or the node replaced) to reset it. The
  [runbook](../runbook.md#pods-on-a-gateway-node-lose-all-cluster-connectivity) carries the recovery.
- If a future backend genuinely needs looser RPF for return traffic, it must scope it to a specific
  uplink interface and **never** to `conf.all`.
