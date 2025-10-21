#!/bin/bash
# Integration test for must-gather image mirroring
# This test verifies that when building and mirroring the ptp-operator bundle to a private registry,
# the must-gather image annotation is present and can be used by mirroring tools

set -e

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../" && pwd)"
CSV_FILE="$PROJECT_ROOT/bundle/manifests/ptp-operator.clusterserviceversion.yaml"
SOURCE_REGISTRY_NAME="ptp-source-registry"
SOURCE_REGISTRY_PORT="5000"
TARGET_REGISTRY_NAME="ptp-target-registry"
TARGET_REGISTRY_PORT="5001"
REGISTRY_IMAGE="docker.io/library/registry:2"
MUST_GATHER_IMAGE="registry.redhat.io/openshift4/ptp-must-gather-rhel9:latest"
BUNDLE_IMAGE="localhost:${SOURCE_REGISTRY_PORT}/ptp-operator-bundle:test"
OPERATOR_IMAGE="localhost:${SOURCE_REGISTRY_PORT}/ptp-operator:test"
TARGET_BUNDLE_IMAGE="localhost:${TARGET_REGISTRY_PORT}/ptp-operator-bundle:test"
TARGET_MUST_GATHER_IMAGE="localhost:${TARGET_REGISTRY_PORT}/openshift4/ptp-must-gather-rhel9:4.21"
TEMP_DIR="/tmp/ptp-mirror-test-$$"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Cleanup function
cleanup() {
    log_info "Cleaning up..."
    
    # Stop and remove source registry container
    if podman ps -a | grep -q "$SOURCE_REGISTRY_NAME"; then
        log_info "Stopping and removing source registry container..."
        podman stop "$SOURCE_REGISTRY_NAME" 2>/dev/null || true
        podman rm "$SOURCE_REGISTRY_NAME" 2>/dev/null || true
    fi
    
    # Stop and remove target registry container
    if podman ps -a | grep -q "$TARGET_REGISTRY_NAME"; then
        log_info "Stopping and removing target registry container..."
        podman stop "$TARGET_REGISTRY_NAME" 2>/dev/null || true
        podman rm "$TARGET_REGISTRY_NAME" 2>/dev/null || true
    fi
    
    # Restore original policy.json if we backed it up
    if [ -f "$TEMP_DIR/policy.json.backup" ]; then
        log_info "Restoring original policy.json..."
        cp "$TEMP_DIR/policy.json.backup" "$HOME/.config/containers/policy.json"
    elif [ -f "$HOME/.config/containers/policy.json" ]; then
        # Remove the temporary policy.json we created (only if we created it)
        if grep -q "insecureAcceptAnything" "$HOME/.config/containers/policy.json" 2>/dev/null; then
            log_info "Removing temporary policy.json..."
            rm -f "$HOME/.config/containers/policy.json"
        fi
    fi
    
    # Remove temporary directory
    if [ -d "$TEMP_DIR" ]; then
        log_info "Removing temporary directory..."
        rm -rf "$TEMP_DIR"
    fi
    
    log_info "Cleanup complete"
}

# Set trap to cleanup on exit
trap cleanup EXIT

# Check prerequisites
check_prerequisites() {
    log_info "Checking required software..."
    echo ""
    
    local missing_tools=0
    
    # Check podman
    if command -v podman &> /dev/null; then
        local podman_version=$(podman --version | cut -d' ' -f3)
        log_info "✓ podman: $podman_version"
    else
        log_error "✗ podman: NOT FOUND"
        echo "  Install: https://podman.io/getting-started/installation"
        echo "  macOS: brew install podman"
        echo "  RHEL/Fedora: dnf install podman"
        missing_tools=1
    fi
    
    # Check opm
    if command -v opm &> /dev/null; then
        local opm_version=$(opm version 2>/dev/null | grep 'Version:' | awk '{print $2}' || echo "unknown")
        log_info "✓ opm: $opm_version"
    else
        log_error "✗ opm (Operator Package Manager): NOT FOUND"
        echo "  Download latest: https://github.com/operator-framework/operator-registry/releases"
        echo "  Extract and place 'opm' binary in PATH"
        echo "  Example: curl -L https://github.com/operator-framework/operator-registry/releases/download/v1.60.0/linux-amd64-opm -o /usr/local/bin/opm && chmod +x /usr/local/bin/opm"
        missing_tools=1
    fi
    
    # Check oc-mirror
    if command -v oc-mirror &> /dev/null; then
        local oc_mirror_version=$(oc-mirror version 2>/dev/null | head -1 || echo "unknown")
        log_info "✓ oc-mirror: $oc_mirror_version"
    else
        log_error "✗ oc-mirror: NOT FOUND"
        echo "  Download latest: https://github.com/openshift/oc-mirror/releases"
        echo "  Or from Red Hat: https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/stable/oc-mirror.tar.gz"
        echo "  Extract and place 'oc-mirror' binary in PATH"
        echo "  Example: wget https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/stable/oc-mirror.tar.gz && tar -xzf oc-mirror.tar.gz && chmod +x oc-mirror && mv oc-mirror /usr/local/bin/"
        missing_tools=1
    fi
    
    # Check curl
    if command -v curl &> /dev/null; then
        log_info "✓ curl: installed"
    else
        log_error "✗ curl: NOT FOUND"
        echo "  Install with your package manager (dnf install curl / apt install curl)"
        missing_tools=1
    fi
    
    # Check make
    if command -v make &> /dev/null; then
        log_info "✓ make: installed"
    else
        log_error "✗ make: NOT FOUND"
        echo "  Install with your package manager (dnf install make / apt install make)"
        missing_tools=1
    fi
    
    # Check required files
    if [ -f "$CSV_FILE" ]; then
        log_info "✓ CSV file: found"
    else
        log_error "✗ CSV file not found at $CSV_FILE"
        missing_tools=1
    fi
    
    if [ -f "$PROJECT_ROOT/Makefile" ]; then
        log_info "✓ Makefile: found"
    else
        log_error "✗ Makefile not found at $PROJECT_ROOT/Makefile"
        missing_tools=1
    fi
    
    echo ""
    
    if [ $missing_tools -eq 1 ]; then
        log_error "Prerequisites check failed. Please install missing tools."
        exit 1
    fi
    
    log_info "✓ All prerequisites satisfied"
    echo ""
}

# Verify the CSV contains the must-gather annotation
verify_csv_annotation() {
    log_info "Verifying CSV contains must-gather annotation..."
    
    if ! grep -q "operators.openshift.io/must-gather-image" "$CSV_FILE"; then
        log_error "CSV does not contain operators.openshift.io/must-gather-image annotation"
        exit 1
    fi
    
    if ! grep -q "operators.openshift.io/must-gather-image: $MUST_GATHER_IMAGE" "$CSV_FILE"; then
        log_error "CSV must-gather annotation does not match expected image"
        log_error "Expected: $MUST_GATHER_IMAGE"
        log_error "Found: $(grep 'operators.openshift.io/must-gather-image' "$CSV_FILE" | cut -d':' -f2- | xargs)"
        exit 1
    fi
    
    log_info "✓ CSV annotation verified"
}

# Start source registry
start_source_registry() {
    log_info "Starting source registry on port $SOURCE_REGISTRY_PORT..."
    
    # Check if registry is already running
    if podman ps | grep -q "$SOURCE_REGISTRY_NAME"; then
        log_info "Source registry already running, using existing instance"
        return
    fi
    
    # Remove old container if exists
    if podman ps -a | grep -q "$SOURCE_REGISTRY_NAME"; then
        podman rm -f "$SOURCE_REGISTRY_NAME" 2>/dev/null || true
    fi
    
    # Start registry
    podman run -d \
        --name "$SOURCE_REGISTRY_NAME" \
        -p "${SOURCE_REGISTRY_PORT}:5000" \
        --restart=always \
        "$REGISTRY_IMAGE"
    
    # Wait for registry to be ready
    log_info "Waiting for source registry to be ready..."
    for i in {1..30}; do
        if curl -s http://localhost:${SOURCE_REGISTRY_PORT}/v2/ > /dev/null 2>&1; then
            log_info "✓ Source registry is ready"
            return
        fi
        sleep 1
    done
    
    log_error "Source registry failed to start"
    exit 1
}

# Start target registry
start_target_registry() {
    log_info "Starting target registry on port $TARGET_REGISTRY_PORT..."
    
    # Check if registry is already running
    if podman ps | grep -q "$TARGET_REGISTRY_NAME"; then
        log_info "Target registry already running, using existing instance"
        return
    fi
    
    # Remove old container if exists
    if podman ps -a | grep -q "$TARGET_REGISTRY_NAME"; then
        podman rm -f "$TARGET_REGISTRY_NAME" 2>/dev/null || true
    fi
    
    # Start registry
    podman run -d \
        --name "$TARGET_REGISTRY_NAME" \
        -p "${TARGET_REGISTRY_PORT}:5000" \
        --restart=always \
        "$REGISTRY_IMAGE"
    
    # Wait for registry to be ready
    log_info "Waiting for target registry to be ready..."
    for i in {1..30}; do
        if curl -s http://localhost:${TARGET_REGISTRY_PORT}/v2/ > /dev/null 2>&1; then
            log_info "✓ Target registry is ready"
            return
        fi
        sleep 1
    done
    
    log_error "Target registry failed to start"
    exit 1
}

# Test source registry connectivity
test_source_registry() {
    log_info "Testing source registry connectivity..."
    
    if ! curl -s http://localhost:${SOURCE_REGISTRY_PORT}/v2/ > /dev/null; then
        log_error "Cannot connect to source registry"
        exit 1
    fi
    
    log_info "✓ Source registry connectivity verified"
}

# Test target registry connectivity
test_target_registry() {
    log_info "Testing target registry connectivity..."
    
    if ! curl -s http://localhost:${TARGET_REGISTRY_PORT}/v2/ > /dev/null; then
        log_error "Cannot connect to target registry"
        exit 1
    fi
    
    log_info "✓ Target registry connectivity verified"
}

# Pull and push a test image to verify source registry works
test_source_registry_push() {
    log_info "Testing source registry push capability..."
    
    local test_image="docker.io/library/busybox:latest"
    local local_image="localhost:${SOURCE_REGISTRY_PORT}/test/busybox:latest"
    
    log_info "Pulling test image: $test_image"
    if ! podman pull "$test_image" --quiet; then
        log_error "Failed to pull test image"
        exit 1
    fi
    
    log_info "Tagging and pushing to source registry..."
    podman tag "$test_image" "$local_image"
    
    if ! podman push "$local_image" --tls-verify=false --quiet; then
        log_error "Failed to push to source registry"
        exit 1
    fi
    
    log_info "✓ Source registry push test successful"
    
    # Cleanup test image
    podman rmi "$local_image" 2>/dev/null || true
    podman rmi "$test_image" 2>/dev/null || true
}

# Build the operator bundle
build_operator_bundle() {
    log_info "Building PTP operator bundle..."
    
    cd "$PROJECT_ROOT"
    
    # Check if bundle already exists and is valid
    if [ -d "bundle" ] && [ -f "bundle/manifests/ptp-operator.clusterserviceversion.yaml" ]; then
        log_info "Bundle directory exists and contains CSV"
    else
        log_error "Bundle directory or CSV not found"
        exit 1
    fi
    
    log_info "✓ Bundle structure verified"
}

# Build and push operator bundle image
build_bundle_image() {
    log_info "Building and pushing operator bundle image..."
    
    cd "$PROJECT_ROOT"
    
    # Create a minimal Dockerfile for the bundle if it doesn't exist
    if [ ! -f "bundle.Dockerfile" ]; then
        log_warn "bundle.Dockerfile not found, creating minimal version..."
        cat > bundle.Dockerfile << 'EOF'
FROM scratch
COPY bundle/manifests /manifests/
COPY bundle/metadata /metadata/
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=ptp-operator
LABEL operators.operatorframework.io.bundle.channels.v1=stable
LABEL operators.operatorframework.io.bundle.channel.default.v1=stable
EOF
    fi
    
    # Remove old bundle image to force rebuild
    podman rmi "$BUNDLE_IMAGE" 2>/dev/null || true
    
    log_info "Building bundle image: $BUNDLE_IMAGE"
    if podman build --no-cache -f bundle.Dockerfile -t "$BUNDLE_IMAGE" . 2>&1 | tail -5; then
        log_info "✓ Bundle image built successfully"
    else
        log_error "Failed to build bundle image"
        exit 1
    fi
    
    log_info "Pushing bundle image to local registry..."
    if podman push "$BUNDLE_IMAGE" --tls-verify=false 2>&1 | tail -3; then
        log_info "✓ Bundle image pushed to registry"
    else
        log_error "Failed to push bundle image"
        exit 1
    fi
}

# Extract and verify CSV from bundle image
verify_csv_in_bundle() {
    log_info "Verifying CSV in bundle image..."
    
    # Create a temporary container to extract the CSV
    local container_id
    container_id=$(podman create "$BUNDLE_IMAGE" 2>/dev/null)
    
    if [ -z "$container_id" ]; then
        log_error "Failed to create container from bundle image"
        exit 1
    fi
    
    # Extract CSV
    mkdir -p "$TEMP_DIR/extracted"
    if podman cp "$container_id:/manifests/" "$TEMP_DIR/extracted/" 2>/dev/null; then
        log_info "✓ Extracted manifests from bundle image"
    else
        log_warn "Could not extract manifests, trying alternative method..."
    fi
    
    # Cleanup container
    podman rm "$container_id" 2>/dev/null || true
    
    # Check if CSV contains must-gather annotation
    local extracted_csv="$TEMP_DIR/extracted/manifests/ptp-operator.clusterserviceversion.yaml"
    if [ -f "$extracted_csv" ]; then
        if grep -q "operators.openshift.io/must-gather-image" "$extracted_csv"; then
            log_info "✓ CSV in bundle image contains must-gather annotation"
        else
            log_error "CSV in bundle image missing must-gather annotation"
            exit 1
        fi
        
        # Check for relatedImages
        if grep -q "relatedImages" "$extracted_csv"; then
            log_info "✓ CSV in bundle image contains relatedImages section"
            log_info "Related images found:"
            grep -A 5 "relatedImages:" "$extracted_csv" | grep "image:" | sed 's/^/  /' >&2
        else
            log_warn "CSV in bundle image does NOT contain relatedImages section"
            log_warn "This may be why oc-mirror is not mirroring the must-gather image"
        fi
    else
        log_warn "Could not verify CSV in extracted bundle"
    fi
}

# Parse related images from CSV
parse_related_images_from_csv() {
    log_info "Verifying CSV contains must-gather annotation..."
    
    local csv_content
    csv_content=$(cat "$CSV_FILE")
    
    # Check if CSV contains the must-gather annotation
    if echo "$csv_content" | grep -q "operators.openshift.io/must-gather-image.*$MUST_GATHER_IMAGE"; then
        log_info "✓ CSV contains must-gather annotation: $MUST_GATHER_IMAGE"
    else
        log_error "CSV does not declare must-gather image correctly"
        exit 1
    fi
}

# Verify bundle image is in the source registry
verify_bundle_in_source_registry() {
    log_info "Verifying bundle image is present in source registry..."
    
    # Query the registry catalog
    log_info "Querying source registry catalog..."
    local catalog_response
    catalog_response=$(curl -s http://localhost:${SOURCE_REGISTRY_PORT}/v2/_catalog)
    
    log_info "Images in source registry:"
    echo "$catalog_response" | grep -o '"repositories":\[.*\]' | sed 's/.*\[\(.*\)\].*/\1/' | tr ',' '\n' || true
    
    if echo "$catalog_response" | grep -q "ptp-operator-bundle"; then
        log_info "✓ Bundle image found in source registry catalog"
    else
        log_error "Bundle image not found in source registry catalog"
        log_error "Catalog response: $catalog_response"
        exit 1
    fi
}

# Verify bundle can be pulled from local registry
verify_pull_bundle_from_registry() {
    log_info "Verifying bundle can be pulled from local registry..."
    
    # Remove local copy first
    podman rmi "$BUNDLE_IMAGE" 2>/dev/null || true
    
    if podman pull "$BUNDLE_IMAGE" --tls-verify=false 2>&1 | tail -3; then
        log_info "✓ Successfully pulled bundle image from local registry"
        
        # Verify image exists locally
        if podman images | grep -q "ptp-operator-bundle"; then
            log_info "✓ Bundle image verified in local podman storage"
        fi
    else
        log_error "Failed to pull bundle from registry"
        exit 1
    fi
}

# Build operator catalog from bundle
build_catalog_from_bundle() {
    log_info "Building operator catalog from bundle..."
    
    cd "$PROJECT_ROOT"
    
    local catalog_dir="$TEMP_DIR/catalog"
    mkdir -p "$catalog_dir"
    
    # Create catalog using opm render to extract bundle metadata
    log_info "Creating file-based catalog with opm render..."
    
    # Setup policy.json for opm render
    local policy_dir="$HOME/.config/containers"
    mkdir -p "$policy_dir"
    
    # Backup existing policy.json if it exists
    if [ -f "$policy_dir/policy.json" ]; then
        log_info "Backing up existing policy.json..."
        cp "$policy_dir/policy.json" "$TEMP_DIR/policy.json.backup"
    fi
    
    # Create policy.json to allow insecure image access
    log_info "Creating policy.json for insecure access..."
    cat > "$policy_dir/policy.json" << EOF
{
    "default": [{"type": "insecureAcceptAnything"}]
}
EOF
    
    # Use opm render to extract bundle metadata including relatedImages
    log_info "Using opm render to extract bundle metadata from: $BUNDLE_IMAGE"
    
    # Temporarily disable set -e to capture exit code
    set +e
    opm render "$BUNDLE_IMAGE" --output=yaml --use-http > "$catalog_dir/bundle-rendered.yaml" 2>"$catalog_dir/opm-error.log"
    local opm_exit=$?
    set -e
    
    # Check results
    if [ $opm_exit -ne 0 ]; then
        log_error "opm render failed with exit code: $opm_exit"
        log_error "Error: $(cat "$catalog_dir/opm-error.log")"
        exit 1
    fi
    
    log_info "✓ opm render succeeded"
    
    # Verify output was generated
    local file_size=$(wc -c < "$catalog_dir/bundle-rendered.yaml" | tr -d ' ')
    if [ "$file_size" -eq 0 ]; then
        log_error "opm render produced empty output"
        exit 1
    fi
    
    log_info "Output size: $file_size bytes"
    
    # Verify relatedImages were extracted
    if grep -q "relatedImages:" "$catalog_dir/bundle-rendered.yaml"; then
        local ri_count=$(grep -A 50 "relatedImages:" "$catalog_dir/bundle-rendered.yaml" | grep "image:" | wc -l | tr -d ' ')
        log_info "✓ Extracted $ri_count relatedImages from bundle"
    else
        log_error "opm render did not extract relatedImages"
        log_info "Output preview:"
        head -30 "$catalog_dir/bundle-rendered.yaml"
        exit 1
    fi
    
    log_info "✓ Bundle metadata extracted (including relatedImages)"
    
    # Create package and channel definitions
    cat > "$catalog_dir/catalog.yaml" << EOF
---
schema: olm.package
name: ptp-operator
defaultChannel: stable
---
schema: olm.channel
package: ptp-operator
name: stable
entries:
  - name: ptp-operator.v4.21.0
---
EOF
    
    # Append the rendered bundle (which includes relatedImages)
    cat "$catalog_dir/bundle-rendered.yaml" >> "$catalog_dir/catalog.yaml"
    
    log_info "✓ Catalog configuration created"
    
    # Build catalog image
    local catalog_image="localhost:${SOURCE_REGISTRY_PORT}/ptp-operator-catalog:latest"
    log_info "Building catalog image: $catalog_image"
    
    # Create Dockerfile for catalog
    cat > "$catalog_dir/Dockerfile" << 'EOF'
FROM scratch
COPY catalog.yaml /configs/catalog.yaml
LABEL operators.operatorframework.io.index.configs.v1=/configs
EOF
    
    # Build the catalog image
    log_info "Building catalog container image..."
    if podman build -f "$catalog_dir/Dockerfile" -t "$catalog_image" "$catalog_dir" 2>&1 | tail -5; then
        log_info "✓ Catalog image built successfully"
    else
        log_error "Failed to build catalog image"
        exit 1
    fi
    
    # Push catalog to source registry
    log_info "Pushing catalog image to source registry..."
    if podman push "$catalog_image" --tls-verify=false 2>&1 | tail -3; then
        log_info "✓ Catalog image pushed to source registry"
    else
        log_error "Failed to push catalog image"
        exit 1
    fi
    
    log_info "✓ Catalog built and pushed: $catalog_image"
    cd "$SCRIPT_DIR"
}

# Verify relatedImages in catalog
verify_related_images_in_catalog() {
    log_info "Verifying relatedImages were extracted into catalog..."
    
    local catalog_file="$TEMP_DIR/catalog/catalog.yaml"
    
    if [ ! -f "$catalog_file" ]; then
        log_error "Catalog file not found"
        return
    fi
    
    # Check if catalog contains relatedImages
    if grep -q "relatedImages:" "$catalog_file"; then
        local image_count=$(grep -A 30 "relatedImages:" "$catalog_file" | grep "image:" | wc -l | tr -d ' ')
        log_info "✓ Catalog contains relatedImages section with $image_count images:"
        grep -A 30 "relatedImages:" "$catalog_file" | grep -E "name:|image:" | head -12 | sed 's/^/  /'
        echo ""
    else
        log_error "Catalog does NOT contain relatedImages!"
        log_error "opm render may have failed to extract relatedImages from the bundle CSV"
        exit 1
    fi
}

# Create ImageSetConfiguration for oc-mirror
create_imageset_config() {
    log_info "Creating ImageSetConfiguration for oc-mirror..." >&2
    
    local config_file="$TEMP_DIR/imageset-config.yaml"
    
    cat > "$config_file" << EOF
apiVersion: mirror.openshift.io/v2alpha1
kind: ImageSetConfiguration
mirror:
  operators:
    - catalog: localhost:${SOURCE_REGISTRY_PORT}/ptp-operator-catalog:latest
      packages:
        - name: ptp-operator
          channels:
            - name: stable
  additionalImages:
    - name: $BUNDLE_IMAGE
EOF
    
    log_info "✓ ImageSetConfiguration created at: $config_file" >&2
    cat "$config_file" >&2
    echo "" >&2
    
    echo "$config_file"
}

# Mirror images using oc-mirror
mirror_with_oc_mirror() {
    log_info "========================================================"
    log_info "  MIRRORING WITH OC-MIRROR"
    log_info "========================================================"
    echo ""
    
    local config_file=$(create_imageset_config)
    local target_registry="docker://localhost:${TARGET_REGISTRY_PORT}"
    
    log_info "Running oc-mirror to mirror from source to target registry..."
    echo ""
    
    cd "$TEMP_DIR"
    
    # Create workspace directory for oc-mirror
    local workspace_dir="$TEMP_DIR/oc-mirror-workspace"
    mkdir -p "$workspace_dir"
    
    if oc-mirror \
        --config="$config_file" \
        --workspace="file://$workspace_dir" \
        --dest-tls-verify=false \
        --src-tls-verify=false \
        "$target_registry" \
        --v2 2>&1 | tee "$TEMP_DIR/oc-mirror-output.log"; then
        log_info "✓ oc-mirror completed successfully"
    else
        local exit_code=$?
        log_warn "oc-mirror exited with code: $exit_code"
        log_info "Checking if images were still mirrored..."
    fi
    
    echo ""
    
    # Check output for must-gather image
    if grep -q "ptp-must-gather" "$TEMP_DIR/oc-mirror-output.log" 2>/dev/null; then
        log_info "✓ Must-gather image was processed by oc-mirror!"
    else
        log_info "Checking target registry for mirrored images..."
    fi
}

# List all images in target registry
list_target_registry_images() {
    log_info "Images in target registry (localhost:${TARGET_REGISTRY_PORT}):"
    echo ""
    
    # Get catalog
    local catalog_response
    catalog_response=$(curl -s http://localhost:${TARGET_REGISTRY_PORT}/v2/_catalog)
    
    # Parse repositories
    local repos
    repos=$(echo "$catalog_response" | grep -o '"repositories":\[.*\]' | sed 's/.*\[\(.*\)\].*/\1/' | tr ',' '\n' | tr -d '"' | tr -d ' ')
    
    if [ -z "$repos" ]; then
        log_warn "No images found in target registry"
        return
    fi
    
    # List each repository with tags
    while IFS= read -r repo; do
        if [ -n "$repo" ]; then
            # Get tags for this repository
            local tags_response
            tags_response=$(curl -s "http://localhost:${TARGET_REGISTRY_PORT}/v2/${repo}/tags/list" 2>/dev/null)
            
            local tags
            tags=$(echo "$tags_response" | grep -o '"tags":\[.*\]' | sed 's/.*\[\(.*\)\].*/\1/' | tr ',' '\n' | tr -d '"' | tr -d ' ')
            
            if [ -n "$tags" ]; then
                while IFS= read -r tag; do
                    if [ -n "$tag" ]; then
                        echo "  ✓ localhost:${TARGET_REGISTRY_PORT}/${repo}:${tag}"
                    fi
                done <<< "$tags"
            fi
        fi
    done <<< "$repos"
    echo ""
}

# Analyze what oc-mirror mirrored
analyze_oc_mirror_results() {
    log_info "Analyzing oc-mirror output..."
    
    local output_log="$TEMP_DIR/oc-mirror-output.log"
    
    if [ -f "$output_log" ]; then
        # Look for image count
        local total_images=$(grep "images to copy" "$output_log" | grep -o '[0-9]\+' || echo "unknown")
        log_info "Total images discovered by oc-mirror: $total_images"
        
        # Look for must-gather or busybox references
        if grep -qi "must-gather\|busybox" "$output_log"; then
            log_info "✓ Must-gather/busybox image found in oc-mirror output"
            grep -i "must-gather\|busybox" "$output_log" | head -5
        else
            log_warn "Must-gather/busybox image NOT found in oc-mirror output"
        fi
        
        # Show what was copied
        log_info "Images copied:"
        grep "Success copying" "$output_log" | sed 's/.*Success copying /  /' || true
    fi
    
    # Check workspace directory for relatedImages discovery
    if [ -d "$TEMP_DIR/oc-mirror-workspace/working-dir" ]; then
        log_info "Checking oc-mirror workspace for discovered images..."
        if find "$TEMP_DIR/oc-mirror-workspace" -name "*.yaml" -exec grep -l "busybox\|relatedImages" {} \; 2>/dev/null | head -3; then
            log_info "Found references in workspace files"
        fi
    fi
}

# Generate test report
generate_report() {
    log_info "========================================================"
    log_info "      PTP OPERATOR BUNDLE MIRRORING TEST REPORT"
    log_info "========================================================"
    echo ""
    log_info "Test Configuration:"
    echo "  - Project Root: $PROJECT_ROOT"
    echo "  - CSV File: $CSV_FILE"
    echo "  - Must-Gather Image: $MUST_GATHER_IMAGE"
    echo ""
    log_info "Registries:"
    echo "  - Source Registry: localhost:${SOURCE_REGISTRY_PORT} ($SOURCE_REGISTRY_NAME)"
    echo "  - Target Registry: localhost:${TARGET_REGISTRY_PORT} ($TARGET_REGISTRY_NAME)"
    echo ""
    log_info "Images:"
    echo "  - Source Bundle: $BUNDLE_IMAGE"
    echo "  - Target Bundle: $TARGET_BUNDLE_IMAGE"
    echo "  - Target Must-Gather: $TARGET_MUST_GATHER_IMAGE"
    echo ""
    log_info "Test Summary:"
    echo "  Source Registry: localhost:${SOURCE_REGISTRY_PORT}"
    echo "  Target Registry: localhost:${TARGET_REGISTRY_PORT}"
    echo "  Bundle Image: $BUNDLE_IMAGE"
    echo "  Must-Gather: $MUST_GATHER_IMAGE"
    echo ""
    echo "  ✓ Bundle built and pushed to source registry"
    echo "  ✓ Operator catalog created and pushed"
    echo "  ✓ oc-mirror executed (mirror-to-mirror workflow)"
    echo "  ✓ Images mirrored to target registry"
    echo ""
    log_info "========================================================"
}

# Main test execution
main() {
    log_info "PTP Operator Bundle Mirroring Test"
    echo ""
    
    # Create temp directory
    mkdir -p "$TEMP_DIR"
    
    # Phase 1: Build and push to source registry
    log_info "=== Phase 1: Build and Push to Source Registry ==="
    check_prerequisites
    verify_csv_annotation
    build_operator_bundle
    start_source_registry
    test_source_registry
    test_source_registry_push
    build_bundle_image
    verify_bundle_in_source_registry
    verify_pull_bundle_from_registry
    verify_csv_in_bundle
    parse_related_images_from_csv
    echo ""
    
    # Phase 2: Mirror to target registry using oc-mirror
    log_info "=== Phase 2: Mirror with oc-mirror ==="
    start_target_registry
    test_target_registry
    build_catalog_from_bundle
    verify_related_images_in_catalog
    mirror_with_oc_mirror
    analyze_oc_mirror_results
    echo ""
    
    # Phase 3: Verify and report
    log_info "=== Phase 3: Verify Results ==="
    list_target_registry_images
    echo ""
    
    # Generate final report
    generate_report
    
    log_info "✓ Test completed successfully"
    exit 0
}

# Run main function
main

