#!/bin/bash
set -x
set -euo pipefail

ARCH=$(uname -m)
if [[ "$ARCH" == "x86_64" ]]; then
    GOARCH="amd64"
elif [[ "$ARCH" == "aarch64" ]]; then
    GOARCH="arm64"
else
    echo "Unsupported architecture: $ARCH"
    exit 1
fi

# Detect OS family
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS_ID="${ID:-unknown}"
else
    OS_ID="unknown"
fi

case "$OS_ID" in
    ubuntu|debian|linuxmint|pop) OS_FAMILY="debian" ;;
    fedora|rhel|centos|rocky|alma) OS_FAMILY="redhat" ;;
    *) echo "WARNING: Unknown OS '$OS_ID', assuming redhat-like"; OS_FAMILY="redhat" ;;
esac

if [[ "${DKMS_MODE:-}" == "true" ]]; then
    if [[ "$OS_FAMILY" == "debian" ]]; then
        apt-get update -qq
        # Pending kernel header packages may trigger DKMS autoinstall for
        # non-running kernels, which fails if the DKMS module can't build
        # against that kernel. Allow apt-get to exit non-zero from the DKMS
        # hook, but verify every required package actually installed.
        # Keep skopeo out of the required set: on Ubuntu CI it can pull
        # containers-common with unmet oci-runtime deps and abort the whole
        # apt transaction.
        REQUIRED_PKGS=(podman pciutils git openssl)
        apt-get install --fix-broken -y "${REQUIRED_PKGS[@]}" || true
        for pkg in "${REQUIRED_PKGS[@]}"; do
            status=$(dpkg-query -W -f='${db:Status-Abbrev}' "$pkg" 2>/dev/null || echo "missing")
            if [[ "$status" != "ii "* ]]; then
                echo "ERROR: required package '$pkg' is not fully installed (status: '$status')"
                exit 1
            fi
        done
        # Optional: deploy-prometheus falls back to podman when skopeo is absent.
        apt-get install --fix-broken -y skopeo || \
            echo "WARNING: skopeo not installed; deploy-prometheus will use podman fallback"
    else
        dnf install -y podman pciutils git openssl
        dnf install -y skopeo || \
            echo "WARNING: skopeo not installed; deploy-prometheus will use podman fallback"
    fi

    # kubectl
    KUBECTL_VER=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
    curl -fsSLo /usr/local/bin/kubectl \
        "https://dl.k8s.io/release/${KUBECTL_VER}/bin/linux/${GOARCH}/kubectl"
    chmod +x /usr/local/bin/kubectl

    # helm
    curl -fsSL "https://get.helm.sh/helm-v3.17.3-linux-${GOARCH}.tar.gz" \
        | tar -C /usr/local/bin --strip-components=1 -xzf - "linux-${GOARCH}/helm"
else
    yum install -y podman pciutils helm

    echo "Installing kubectl/oc for $ARCH"
    _tmp="${PTP_RUN_DIR:-/tmp}"
    OC_TARBALL="$(mktemp "${_tmp}/openshift-client-linux.XXXXXX.tar.gz")"
    if [[ "$ARCH" == "x86_64" ]]; then
        curl -Lo "${OC_TARBALL}" "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux.tar.gz"
    elif [[ "$ARCH" == "aarch64" ]]; then
        curl -Lo "${OC_TARBALL}" "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux-arm64.tar.gz"
    fi
    OC_TMP_DIR="$(mktemp -d "${_tmp}/openshift-client.XXXXXX")"
    trap 'rm -rf "${OC_TMP_DIR:-}" "${OC_TARBALL:-}"' EXIT
    tar -xf "${OC_TARBALL}" -C "${OC_TMP_DIR}" oc kubectl
    sudo install -m 0755 "${OC_TMP_DIR}/oc" "${OC_TMP_DIR}/kubectl" /usr/local/bin/
    rm -f "${OC_TARBALL}"
    oc version || true
fi

# Install Go
GO_VERSION=$(curl -s https://go.dev/VERSION?m=text | head -n 1)

echo "Installing Go $GO_VERSION for $GOARCH"

GO_TARBALL="$(mktemp "${PTP_RUN_DIR:-/tmp}/go.XXXXXX.tar.gz")"
curl -Lo "${GO_TARBALL}" "https://go.dev/dl/${GO_VERSION}.linux-${GOARCH}.tar.gz"

INSTALL_DIR="/usr/local"
sudo rm -rf "${INSTALL_DIR}/go"
sudo tar -C "${INSTALL_DIR}" -xzf "${GO_TARBALL}"
rm -f "${GO_TARBALL}"

if ! grep -q 'export PATH=$PATH:"$HOME"/go/bin:/usr/local/go/bin' ~/.bashrc; then
    echo 'export PATH=$PATH:"$HOME"/go/bin:/usr/local/go/bin' >>~/.bashrc
    echo "Go path added to ~/.bashrc. Run 'source ~/.bashrc' or restart your shell."
fi

export BASHRCSOURCED=1
PS1="${PS1:-}" source ~/.bashrc

# Install ginkgo
go mod tidy
go mod vendor
go install github.com/onsi/ginkgo/v2/ginkgo

# Install kind
curl -Lo ./kind "https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-${GOARCH}"
chmod +x ./kind
sudo mv ./kind /usr/bin/kind

# Increase inotify limits for kind
sudo sysctl -w fs.inotify.max_user_instances=512
sudo sysctl -w fs.inotify.max_user_watches=524288
