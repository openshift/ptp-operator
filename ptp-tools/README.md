# PTP Tools - Container Image Builder

This directory contains tools and scripts for building all container images required to run the PTP operator.

## Overview

The ptp-tools Makefile provides targets to build all images required for the PTP operator including:
- `lptpd` - LinuxPTP daemon
- `cep` - Cloud Event Proxy
- `ptpop` - PTP Operator
- `krp` - Kube RBAC Proxy
- `prometheus` - Prometheus (for netdevsim test environment)

All images are built using podman and stored in a single repository using different tags to identify each component.

## Prerequisites

- `podman` must be installed and configured
- Access to a container registry (e.g., quay.io, docker.io)

## Quick Start

### Building All Images

To build all images for your personal repository:

```bash
IMG_PREFIX=quay.io/yourusername/test make podman-buildall
```

This will create the following images:
- `quay.io/yourusername/test:lptpd` - LinuxPTP daemon
- `quay.io/yourusername/test:cep` - Cloud Event Proxy
- `quay.io/yourusername/test:ptpop` - PTP Operator
- `quay.io/yourusername/test:krp` - Kube RBAC Proxy
- `quay.io/yourusername/test:prometheus` - Prometheus (for netdevsim test environment only)

### Pushing All Images

To push all built images to the registry:

```bash
IMG_PREFIX=quay.io/yourusername/test make podman-pushall
```

### Deploying with Custom Images

To deploy the operator using your custom images:

```bash
IMG_PREFIX=quay.io/yourusername/test make deploy-all
```

## Staging Images from Remote Branches

`scripts/build-push-deploy.sh` builds, pushes, and deploys images from **remote** git branches or commits. It does not build local working-tree changes.

Use this when you want to stage a specific upstream/downstream branch (or fork commit) onto a cluster without checking those repos out yourself.

### Prerequisites

- `podman`, `skopeo`, and `kubectl` (cluster kubeconfig already configured)
- Registry credentials for `IMG_PREFIX` (default `quay.io/deliedit/test`)
- At least one of `--ptpop`, `--lptpd`, or `--cep`

### Branch specs

| Format | Meaning | Example |
|--------|---------|---------|
| `upstream/<branch>` | Well-known upstream repo | `upstream/main` |
| `downstream/<branch>` | Well-known OpenShift/downstream repo | `downstream/release-4.22` |
| `downstream/<commit>` | Exact commit (7–40 hex chars) | `downstream/a1b2c3d4e5f6` |
| `<org>/<repo>/<branch>` | Arbitrary GitHub fork | `edcdavid/linuxptp-daemon/my-fix` |

Repo mapping for shorthand:

| Component | `upstream` | `downstream` |
|-----------|------------|--------------|
| `ptpop` | `k8snetworkplumbingwg/ptp-operator` | `openshift/ptp-operator` |
| `lptpd` | `k8snetworkplumbingwg/linuxptp-daemon` | `openshift/linuxptp-daemon` |
| `cep` | `redhat-cne/cloud-event-proxy` | `redhat-cne/cloud-event-proxy` |

### Phases

If no phase flag is given, the script runs **build + push + deploy**.

| Flag | Action |
|------|--------|
| `--build` | Build only the specified component images |
| `--push` | Push those images (deletes the remote tag first so the digest updates) |
| `--deploy` | `make deploy-all`, then force daemonset pull-policy Always and restart |
| `--check` | Verify running pod commits (and optional linuxptp RPM) match the requested specs |

### Custom linuxptp RPM

Pass a pre-built RPM with `--linuxptp-rpm` (requires `--lptpd`). The script copies it into `ptp-tools/extra/` for the image build; `*.rpm` files under `extra/` are gitignored and must not be committed.

```bash
./scripts/build-push-deploy.sh \
  --lptpd upstream/main \
  --linuxptp-rpm /path/to/linuxptp-4.4-1.el9.4.rpm \
  --build --push --deploy
```

`Dockerfile.lptpd` installs the stock `linuxptp` package unless `LINUXPTP_RPM` is set at build time.

### Examples

```bash
# Stage operator + daemon from downstream release branches
./scripts/build-push-deploy.sh \
  --ptpop downstream/release-4.20 \
  --lptpd downstream/release-4.20

# Build CEP from a specific commit only
./scripts/build-push-deploy.sh --cep downstream/a1b2c3d4e5f6 --build --push

# Fork branch, custom registry prefix
./scripts/build-push-deploy.sh \
  --cep edcdavid/cloud-event-proxy/fix-tbc \
  --img-prefix quay.io/yourusername/test \
  --build --push --deploy

# Verify what is running after a deploy
./scripts/build-push-deploy.sh --lptpd upstream/main --check
```

### How it works

1. Rewrites the relevant `Dockerfile.*` clone lines to the requested remote/branch/commit (originals restored on exit).
2. Optionally copies a linuxptp RPM into `ptp-tools/extra/` and passes `PODMAN_BUILD_ARGS=--build-arg=LINUXPTP_RPM=...`.
3. Builds/pushes via the ptp-tools Makefile.
4. On deploy: applies custom images, scales the operator down briefly, sets daemonset pull policies to `Always`, and restarts the daemonset so nodes pull the new tags.

## Platform Support

### Auto-Detection (Default)

By default, the Makefile automatically detects your current platform:

```bash
# Builds for current platform (e.g., linux/amd64)
IMG_PREFIX=quay.io/yourusername/test make podman-buildall
```

### Single Platform Override

You can override the platform detection by setting the `PLATFORM` environment variable:

```bash
# Build for specific platform
PLATFORM=linux/arm64 IMG_PREFIX=quay.io/yourusername/test make podman-buildall
```

### Multi-Platform Builds

For multi-platform builds (creating manifests that support multiple architectures):

```bash
# Build for multiple platforms
PLATFORM=linux/amd64,linux/arm64 IMG_PREFIX=quay.io/yourusername/test make podman-buildall
```

## Examples

### Example 1: Build for AMD64 and ARM64

```bash
PLATFORM=linux/amd64,linux/arm64 IMG_PREFIX=quay.io/deliedit/test make podman-buildall
```

This command will:
1. Create manifest lists for each image type
2. Build images for both AMD64 and ARM64 architectures
3. Add both architectures to the manifest list

### Example 2: Build Single Architecture

```bash
PLATFORM=linux/arm64 IMG_PREFIX=quay.io/deliedit/test make podman-buildall
```

This builds all images specifically for ARM64 architecture.

### Example 3: Build and Push

```bash
# Build all images
PLATFORM=linux/amd64,linux/arm64 IMG_PREFIX=quay.io/deliedit/test make podman-buildall

# Push all manifests
IMG_PREFIX=quay.io/deliedit/test make podman-pushall
```

## Individual Image Operations

### Building Individual Images

You can build individual images using the pattern `podman-build-<component>`:

```bash
# Build only the PTP operator image
IMG_PREFIX=quay.io/yourusername/test make podman-build-ptpop

# Build only the LinuxPTP daemon image
IMG_PREFIX=quay.io/yourusername/test make podman-build-lptpd

# Build only the Cloud Event Proxy image
IMG_PREFIX=quay.io/yourusername/test make podman-build-cep
```

This creates individual images like:
- `quay.io/yourusername/test:ptpop`
- `quay.io/yourusername/test:lptpd`
- `quay.io/yourusername/test:cep`

### Pushing Individual Images

```bash
# Push only the PTP operator image
IMG_PREFIX=quay.io/yourusername/test make podman-push-ptpop

# Push only the LinuxPTP daemon image
IMG_PREFIX=quay.io/yourusername/test make podman-push-lptpd
```

### Cleaning Individual Images

```bash
# Clean only the PTP operator image
IMG_PREFIX=quay.io/yourusername/test make podman-clean-ptpop

# Clean all images
make podman-cleanall
```


## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `IMG_PREFIX` | `quay.io/<your user id here>/<your image name>` | Container registry and repository prefix |
| `PLATFORM` | Auto-detected | Target platform(s) for builds |
| `PODMAN_BUILD_ARGS` | _(empty)_ | Extra args for `podman build` (e.g. `--build-arg=LINUXPTP_RPM=...`) |

### Final Image Names

When using `IMG_PREFIX=quay.io/yourusername/test`, the following images are created:

| Final Image Name | Component | Description |
|------------------|-----------|-------------|
| `quay.io/yourusername/test:cep` | Cloud Event Proxy | Handles PTP events and cloud event publishing |
| `quay.io/yourusername/test:ptpop` | PTP Operator | Main operator managing PTP configurations |
| `quay.io/yourusername/test:lptpd` | LinuxPTP Daemon | Daemon running PTP processes on nodes |
| `quay.io/yourusername/test:krp` | Kube RBAC Proxy | RBAC proxy for secure access |
| `quay.io/yourusername/test:prometheus` | Prometheus | Monitoring for netdevsim test environment |

### Supported Platforms

- `linux/amd64` - Intel/AMD 64-bit
- `linux/arm64` - ARM 64-bit
- `linux/s390x` - IBM System z
- `linux/ppc64le` - PowerPC 64-bit Little Endian

## Troubleshooting

### Common Issues

1. **Permission denied when pushing to registry**
   ```bash
   podman login quay.io
   ```

2. **Platform not supported**
   - Ensure the base images support your target platform
   - Check that podman supports the requested platform

3. **Build failures**
   - Clean existing images: `make podman-cleanall`
   - Check available disk space
   - Verify network connectivity to base image registries

### Debugging

To see what platform is being used:

```bash
make podman-build-ptpop
```

The platform will be displayed at the beginning of the build process.

## Advanced Usage

### Custom Dockerfile Modifications

Each component has its own Dockerfile:
- `Dockerfile.cep` - Cloud Event Proxy
- `Dockerfile.ptpop` - PTP Operator
- `Dockerfile.lptpd` - LinuxPTP Daemon (optional `LINUXPTP_RPM` build-arg)
- `Dockerfile.krp` - Kube RBAC Proxy
- `Dockerfile.prometheus` - Prometheus

For staging images from remote branches without editing Dockerfiles by hand, prefer `scripts/build-push-deploy.sh` (see [Staging Images from Remote Branches](#staging-images-from-remote-branches)).

### Registry Authentication

For private registries, authenticate before building:

```bash
podman login your-private-registry.com
IMG_PREFIX=your-private-registry.com/yournamespace/test make podman-buildall
```

## Integration with CI/CD

The Makefile is designed to work well in CI/CD environments:

```bash
# Example CI/CD pipeline step
export IMG_PREFIX="quay.io/${CI_PROJECT_NAMESPACE}/test"
export PLATFORM="linux/amd64,linux/arm64"

make podman-buildall
make podman-pushall
```
