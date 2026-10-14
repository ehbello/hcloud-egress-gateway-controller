# ADR-0006: Managed floating IPs are adopted by label and never recreated; Retain by default

**Status:** accepted, 2026-10-14

## Context

A floating IP's address is the thing an upstream allow-lists. Once an address is given to a partner to
filter on, losing it is expensive — it means a coordination round-trip to re-allow-list a new one. The
controller creates these IPs in managed mode, so it must be impossible for a reconcile, a CR deletion,
or a CR recreation to silently mint a new address in place of a working one.

## Decision

- **Keyed by label, not by name.** Each managed IP carries `managed-by=hcloud-egress-gateway-controller`,
  `egress-gateway=<cr>`, `egress-region=<location>`, `egress-index=<i>`. `EnsureManaged` lists by those
  labels and **adopts the match if one exists**, creating only when none does. The name
  `<cr>-<location>-<index>` is cosmetic. Recreating a deleted CR with the same name and regions
  re-adopts the exact same addresses.
- **`reclaimPolicy: Retain` by default.** Deleting the CR leaves managed IPs in Hetzner. `Delete` is
  opt-in, for ephemeral and test gateways only.
- BYO IPs (`spec.floatingIPs`) are adopted as-is and never created or deleted.

## Consequences

- An orphaned floating IP after a careless CR deletion is the deliberate failure mode: a cheap, idle,
  still-allow-listed IP, re-adopted the moment the CR comes back, is far better than a lost address.
- `reclaimPolicy: Delete` on a CR whose addresses an upstream filters on will break that upstream when
  the CR is deleted; the field default and the docs steer away from it.
- Retiring an address for real is a deliberate act: `Delete`, or delete the IP in the Console, and
  remove it from the upstream allow-list ([operations](../operations.md#moving-or-retiring-a-gateway)).
