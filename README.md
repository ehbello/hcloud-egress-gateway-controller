# hcloud-egress-gateway-controller

[![CI](https://github.com/maarlab-rethinking/hcloud-egress-gateway-controller/actions/workflows/ci.yml/badge.svg)](https://github.com/maarlab-rethinking/hcloud-egress-gateway-controller/actions/workflows/ci.yml)

Give selected Kubernetes pods a **stable, fixed egress source IP** — a set of **Hetzner Cloud floating
IPs** — spread across the nodes where the controller's agents run, so upstreams that filter by source
IP accept the traffic. No fixed egress node pool and no external VMs: the egress identity follows the
agent pods wherever the scheduler puts them.

Apache-2.0. Maintained by Maarlab Rethinking.

## Why

On Kubernetes a pod's egress is SNAT'd to its **node** IP, which changes as nodes are recycled by
Cluster API and autoscaling, so any upstream that allow-lists source IPs breaks. This controller
decouples a **stable floating IP** from the node lifecycle and lets services opt in by label, keeping
the platform fully dynamic.

It owns only the half the ecosystem is missing — provisioning the Hetzner floating IP and keeping it
live on a node — and **delegates the actual redirect to the CNI** (Cilium by default). A general egress
gateway does the redirect but cannot provision a routable cloud IP;
[ADR-0001](docs/decisions/0001-floating-ip-controller-not-datapath.md) says why that split is the point.

## Documentation

- [Architecture](docs/architecture.md): what one reconcile and one agent loop do, in order and with
  reasons, and the [decision records](docs/decisions/) for the alternatives that lost.
- [Operations](docs/operations.md): reading the status, installing and upgrading, compatibility, the
  Hetzner token, floating-IP lifecycle and reclaim, regions and locality, backends, moving and retiring.
- [Runbook](docs/runbook.md): symptom first — a low `readyGateways`, a crashlooping agent, traffic not
  using the floating IP, a gateway node losing connectivity — and what to do about each.
- [API reference](docs/api.md), generated from the Go types.

## Install

```console
helm upgrade --install hcloud-egress-gateway charts/hcloud-egress-gateway-controller \
  --namespace egress-system --create-namespace
```

The namespace must allow privileged pods: the agents run `hostNetwork` and `privileged` as root
([ADR-0003](docs/decisions/0003-agent-runs-as-root.md)). The Hetzner token is read from the Secret named
by `hcloud.tokenSecretName`. Every value is in `values.yaml`.

## A gateway

```yaml
apiVersion: egress.maarlab.dev/v1alpha1
kind: HetznerEgressGateway
metadata:
  name: partner-api
spec:
  backend: cilium            # or host-routing
  managed:
    regions:                 # floating IPs per Hetzner location
      - { location: fsn1, count: 1 }
      - { location: nbg1, count: 1 }
    reclaimPolicy: Retain
  podSelector:
    matchLabels:
      egress-via: partner-api
  # destinationCIDRs omitted => any destination (0.0.0.0/0), cluster-internal excluded
```

```console
$ kubectl get hetzneregressgateway
NAME          READY   AGE
partner-api   2       3m
```

A workload opts in by carrying `egress-via: partner-api` on its pods; pods without the label egress
from their node's own IP. The live addresses to allow-list upstream are in `status.floatingIPs[].address`.

## Floating IPs are an allow-listed asset

A managed floating IP is created once and then **adopted by label, never recreated**, and
`reclaimPolicy` defaults to `Retain`, so deleting and recreating the CR keeps the exact same address
([ADR-0006](docs/decisions/0006-managed-floating-ips-adopt-never-recreate.md)). An allow-listed address
is expensive to change; the controller treats losing one as worse than an orphaned IP. Bring your own
pre-allow-listed addresses instead with `spec.floatingIPs`.

## The redirect is the CNI's job

Only the egress redirect is CNI-specific; the floating-IP lifecycle, node assignment, interface and
labels are not. It is a pluggable backend
([ADR-0002](docs/decisions/0002-cilium-default-host-routing-fallback.md)): `cilium` (default) renders
one `CiliumEgressGatewayPolicy` with a gateway per floating IP, in `egressIP` mode, and lets Cilium do
the redirect and SNAT, spread the pods across the floating IPs and fail over between them
([ADR-0009](docs/decisions/0009-cilium-egressip-not-interface.md) says why `egressIP`, not `interface`
on the `egr0` dummy); `host-routing` programs policy routing and `nft` SNAT in the agent itself, for any
CNI. Cilium's OSS egress gateway is **not topology-aware**, so a pod may leave through any of the CR's
floating IPs, whatever its region — which is why every address is allow-listed
([operations](docs/operations.md#regions-and-locality)).

## Development

```console
make test                        # unit tests (fake client + fake hcloud)
make lint vet helm-lint
make generate manifests api-docs # after changing api/ or the +kubebuilder markers
make e2e-up e2e e2e-down CILIUM_VERSION=1.20.3   # datapath e2e on kind (docker, kind, helm)
```

Tools are pinned in the `Makefile`. `make manifests` generates the CRD and the ClusterRole into the
chart's `templates/`, and `make api-docs` the [API reference](docs/api.md), all from the Go source;
never edit those by hand, CI fails on drift ([ADR-0008](docs/decisions/0008-generate-crd-rbac-api-reference.md)).
pre-commit hooks (`pre-commit install`) run the generated-files check, helm lint and the
one-manifest-per-file rule. The datapath e2e runs the rendered policy against a real Cilium on kind,
for every supported minor and the next pre-release, weekly as well as on changes, so a Cilium release
that breaks the egress path is seen before the platform adopts it
([ADR-0010](docs/decisions/0010-cilium-datapath-e2e-matrix.md)). The agent's netlink path and the real
Hetzner client run only on a cluster, not in CI.

CI builds with the newest patch release of the Go minor in `GO_VERSION`, not with the `go` directive
of `go.mod`, which is only the minimum. govulncheck runs on every push and daily, so a vulnerability
fixed in a new Go or dependency release turns it red without a push; Dependabot proposes the
dependency, action and base image bumps weekly. Raise `GO_VERSION` with the Dockerfile's `golang`
image when Go moves to a new minor.

## Status

Alpha (`v1alpha1`); the API is not yet stable. The controller, agent and both backends are implemented
and unit-tested. The default `cilium` backend is the exercised path; `host-routing` is a best-effort
extension point (needs `nft` in the image).
