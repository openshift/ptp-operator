# PTP Operator Bundle Tests

## Must-Gather Image Mirroring Tests

### Overview

These tests validate that the ptp-operator CSV is properly configured for image mirroring in disconnected environments.

### CSV Requirements for Mirroring

For oc-mirror to automatically mirror all operator images:

1. **`metadata.annotations.operators.openshift.io/must-gather-image`**  
   Documents which must-gather image belongs to this operator

2. **`spec.relatedImages`**  
   Lists ALL images that oc-mirror should mirror (operator images + must-gather)

## Tests

### 1. Bundle Must-Gather Validation (CI Presubmit)

**Target**: `make test-bundle-must-gather`

**Purpose**: Validates CSV structure without requiring external tools

**What it checks:**
- ✅ CSV contains `operators.openshift.io/must-gather-image` annotation
- ✅ CSV contains `spec.relatedImages` section
- ✅ Must-gather image is listed in `relatedImages`
- ✅ Counts and validates number of relatedImages

**Requirements:**
- Standard Unix tools (grep, wc)
- No special software needed

**Usage:**
```bash
make test-bundle-must-gather
```

**CI Integration:**
This test runs as a presubmit job in OpenShift CI for releases 4.21+

### 2. Must-Gather Mirroring Integration Test (Local)

**Target**: `make test-must-gather-mirroring`  
**Script**: `bundle/tests/test_must_gather_mirroring.sh`

**Purpose**: End-to-end validation of actual image mirroring with oc-mirror v2

**What it does:**
1. Builds operator bundle image
2. Creates source registry (HTTP, port 5000)
3. Pushes bundle to source registry
4. Uses `opm render` to extract relatedImages from CSV
5. Builds operator catalog
6. Creates target registry (HTTP, port 5001)
7. **Runs real oc-mirror v2** to mirror from source to target
8. Lists all mirrored images in target registry
9. Validates must-gather and operator images were mirrored

**Requirements:**
- `podman` - Container management
- `opm` - Operator Package Manager ([Download](https://github.com/operator-framework/operator-registry/releases))
- `oc-mirror` v2 - Image mirroring tool ([Download](https://docs.openshift.com/container-platform/latest/installing/disconnected_install/installing-mirroring-disconnected.html))
- `curl` - Registry API queries
- `make` - Build tool
- Ports 5000 and 5001 available

**Usage:**
```bash
make test-must-gather-mirroring
# or directly:
cd bundle/tests
./test_must_gather_mirroring.sh
```

**Note**: This test is intended for local development and validation. It requires privileged container access and is not suitable for standard CI environments.

## Test Output Examples

### CI Validation Test:
```
Validating must-gather configuration in CSV...
✓ Must-gather annotation present
✓ CSV contains relatedImages section with 5 images
✓ Must-gather image found in relatedImages
CSV validation passed - operator images will be mirrored correctly
```

### Integration Test:
```
=== Phase 1: Build and Push to Source Registry ===
✓ Bundle built and pushed to source registry
✓ opm render extracted 5 relatedImages from bundle

=== Phase 2: Mirror with oc-mirror ===
✓ oc-mirror discovered 8 images
✓ 4 / 7 operator images mirrored successfully

=== Phase 3: Verify Results ===
Images in target registry (localhost:5001):
  ✓ localhost:5001/openshift/origin-cloud-event-proxy:4.21
  ✓ localhost:5001/openshift/origin-kube-rbac-proxy:4.21
  ✓ localhost:5001/ptp-operator-bundle:test
  ✓ localhost:5001/ptp-operator-catalog:latest
```

## Why This Matters

In disconnected/air-gapped OpenShift environments:
- Operators must be mirrored to a private registry
- oc-mirror reads `spec.relatedImages` from the CSV
- All operator images and must-gather debugging tools are mirrored together
- Without proper configuration, debugging images would be missing

These tests ensure the ptp-operator is correctly configured for disconnected deployments.
