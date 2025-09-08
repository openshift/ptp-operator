# PTP Tools - Container Image Builder

This directory contains tools and scripts for building all container images required to run the PTP operator.

## Overview

The ptp-tools Makefile provides targets to build all images required for the PTP operator including:
- `lptpd` - LinuxPTP daemon
- `cep` - Cloud Event Proxy
- `ptpop` - PTP Operator
- `krp` - Kube RBAC Proxy
- `openvswitch` - OpenVSwitch (for netdevsim test environment)
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
- `quay.io/yourusername/test:openvswitch` - OpenVSwitch (for netdevsim test environment only)
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

### Final Image Names

When using `IMG_PREFIX=quay.io/yourusername/test`, the following images are created:

| Final Image Name | Component | Description |
|------------------|-----------|-------------|
| `quay.io/yourusername/test:cep` | Cloud Event Proxy | Handles PTP events and cloud event publishing |
| `quay.io/yourusername/test:ptpop` | PTP Operator | Main operator managing PTP configurations |
| `quay.io/yourusername/test:lptpd` | LinuxPTP Daemon | Daemon running PTP processes on nodes |
| `quay.io/yourusername/test:krp` | Kube RBAC Proxy | RBAC proxy for secure access |
| `quay.io/yourusername/test:openvswitch` | OpenVSwitch | Network virtualization for netdevsim test environment |
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
- `Dockerfile.lptpd` - LinuxPTP Daemon
- `Dockerfile.krp` - Kube RBAC Proxy
- `Dockerfile.openvswitch` - OpenVSwitch
- `Dockerfile.prometheus` - Prometheus

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
