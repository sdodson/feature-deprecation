# feature-deprecation-controller

A Kubernetes controller for OpenShift that manages `FeatureDeprecation` custom resources. It provides a structured, machine-readable registry of deprecated and removed platform features, and automatically reflects whether the current cluster has reached the removal version.

## Overview

Platform teams create `FeatureDeprecation` objects to document features that have been deprecated or removed. The controller watches the cluster's `ClusterVersion` and sets status conditions on each record indicating:

- **`Removed`** — whether the running cluster version has reached or passed the `removedInVersion`
- **`MigrationDocumented`** — whether a migration path (`replacedBy` or `migrationGuide`) has been documented

This gives operators and tooling a single, consistent source of truth for feature lifecycle state.

## Custom Resource: `FeatureDeprecation`

`FeatureDeprecation` is a cluster-scoped resource (short name: `fd`) in the `deprecation.openshift.io/v1alpha1` API group.

### Spec fields

| Field | Required | Description |
|---|---|---|
| `feature.name` | Yes | Human-readable name of the deprecated feature |
| `feature.description` | No | Longer context about the feature |
| `feature.component` | No | Platform component that owns the feature |
| `phase` | Yes | `Deprecated` (still works, migrate soon) or `Removed` (no longer available) |
| `deprecatedInVersion` | Yes | Platform version when deprecation was announced (`major.minor`) |
| `removedInVersion` | Yes | Platform version when the feature is/was removed (`major.minor`) |
| `reason` | Yes | Explanation of why the feature is being deprecated |
| `replacedBy` | No | Feature or API that supersedes this one |
| `migrationGuide` | No | URL pointing to migration documentation |
| `additionalInfo` | No | Supplemental information not covered by other fields |

### Validation rules

The CRD enforces the following via CEL:

1. `removedInVersion` major must be strictly greater than `deprecatedInVersion` major — features are deprecated in a minor release and removed in the next major release.
2. When `phase=Removed`, either `replacedBy` or `migrationGuide` must be set.

### Status conditions

| Condition | True when | False when | Unknown when |
|---|---|---|---|
| `Removed` | Cluster version >= `removedInVersion` | Cluster version < `removedInVersion` | `ClusterVersion` is unavailable or unparseable |
| `MigrationDocumented` | `replacedBy` or `migrationGuide` is set | Neither field is set | — |

### Example

```yaml
apiVersion: deprecation.openshift.io/v1alpha1
kind: FeatureDeprecation
metadata:
  name: intree-vsphere-volume-plugin
spec:
  feature:
    name: "In-tree vSphere volume plugin"
    description: >
      The legacy in-tree vSphere volume plugin (kubernetes.io/vsphere-volume)
      allowed clusters to provision and attach vSphere VMDK volumes directly
      via the Kubernetes volume subsystem without a separate CSI driver.
    component: storage

  phase: Deprecated

  deprecatedInVersion: "5.2"
  removedInVersion: "6.0"

  reason: >
    The upstream Kubernetes project removed in-tree cloud provider volume
    plugins in favour of the Container Storage Interface (CSI).

  replacedBy: "vSphere CSI driver (kubernetes-sigs/vsphere-csi-driver)"
  migrationGuide: "https://docs.openshift.com/..."
```

Additional examples are in [`config/samples/`](config/samples/).

## Getting started

### Prerequisites

- Go 1.23+
- Access to an OpenShift cluster (for running against a live cluster)
- `kubectl` / `oc` configured with appropriate permissions

### Build

```bash
make build
# Output: bin/feature-deprecation-controller
```

### Run tests

```bash
make test
```

### Install the CRD and run locally

```bash
# Install CRD into the current cluster
make install

# Run the controller locally (uses your current kubeconfig)
make run
```

### Deploy a container image

```bash
make docker-build IMAGE=<registry/image:tag>
make docker-push  IMAGE=<registry/image:tag>
```

### Uninstall

```bash
make uninstall
```

## Development

### Project layout

```
cmd/feature-deprecation-controller/   # Controller entrypoint (main.go)
deprecation/v1alpha1/                 # API types and scheme registration
  types.go                            # CRD spec/status structs with kubebuilder markers
  groupversion_info.go                # Group/version constants and AddToScheme
  zz_generated.deepcopy.go           # Generated — do not edit manually
internal/controller/                  # Reconciliation logic
  featuredeprecation_controller.go    # FeatureDeprecationReconciler
config/crd/bases/                     # Generated CRD manifest (committed)
config/samples/                       # Example FeatureDeprecation resources
hack/boilerplate.go.txt               # License header used by controller-gen
```

### Code generation

After modifying `deprecation/v1alpha1/types.go`, regenerate the derived artifacts:

```bash
make generate    # regenerates zz_generated.deepcopy.go
make manifests   # regenerates CRD YAML in config/crd/bases/
```

Commit the generated files alongside the type changes. `controller-gen` is downloaded automatically to `bin/` on first use.

### Other make targets

```bash
make fmt    # go fmt ./...
make vet    # go vet ./...
```

## Controller design notes

- **ClusterVersion watch**: The controller watches `ClusterVersion` (name=`version`) from `config.openshift.io/v1`. Any change fans out reconcile requests to all `FeatureDeprecation` objects, keeping status current whenever the cluster upgrades.
- **Unstructured client**: `ClusterVersion` is fetched via an unstructured client to avoid importing typed openshift/api clients, keeping the dependency footprint minimal. Version is read from `.status.desired.version`.
- **Version comparison**: Only `major.minor` is compared; patch versions, pre-release identifiers, and build metadata are stripped before comparison.
