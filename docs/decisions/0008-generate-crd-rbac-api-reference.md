# ADR-0008: Generate the CRD, the RBAC ClusterRole and the API reference from source

**Status:** accepted, 2026-10-14

## Context

The chart originally shipped a **hand-maintained** CRD in `templates/crd.yaml` (with a "keep in sync
with api/v1alpha1" comment) while `make manifests` wrote a generated CRD to a `crds/` directory the
chart never served. The ClusterRole was written entirely by hand, with no `+kubebuilder:rbac` markers.
Both drift silently: a field added to the Go types but not copied to the template is rejected at
runtime; a reconcile that starts reading a new resource fails with `forbidden` in the cluster, never in
CI.

## Decision

`make manifests` generates both into the chart's `templates/`, so `helm upgrade` updates them, each
wrapped in its install guard (`.Values.installCRD`, `.Values.rbac.create`):

- the CRD from the `api/` types;
- the ClusterRole from `+kubebuilder:rbac` markers. These **must** be a package-level (floating)
  comment — controller-gen ignores markers attached to a declaration's doc comment and exits 0 having
  written nothing, which would wrap an empty ClusterRole and deploy the operator with no permissions, so
  `make manifests` fails hard (`test -s`) if controller-gen produces no CRD or no `role.yaml`.

`make api-docs` generates `docs/api.md` from the types with crd-ref-docs. The leader-election Leases and
the Hetzner token Secret stay in a hand-written namespaced Role, never in the generated ClusterRole.

A CI job runs `make generate manifests api-docs` and fails on any `git status` change, so none of the
generated files can drift from source. pre-commit runs the same checks plus one-manifest-per-file and
helm lint.

## Consequences

- `templates/crd.yaml`, `templates/clusterrole.yaml` and `docs/api.md` are generated artifacts; edit the
  Go types and the markers, never the files. The chart is laid out one manifest per file.
- The CRD's `status` became a typed schema (from the Go types) instead of a hand-written
  preserve-unknown block; it is backward-compatible with stored objects.
- The build fails loudly, at generation time, if the rbac markers are misplaced — the one way this class
  of mistake used to reach a cluster silently.
