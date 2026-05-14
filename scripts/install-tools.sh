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

if [[ "${DKMS_MODE:-}" == "true" ]]; then
    apt-get update
    apt-get install -y podman pciutils openvswitch-switch git openssl

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
    if [[ "$ARCH" == "x86_64" ]]; then
        curl -Lo ./openshift-client-linux.tar.gz "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux.tar.gz"
    elif [[ "$ARCH" == "aarch64" ]]; then
        curl -Lo openshift-client-linux.tar.gz "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux-arm64.tar.gz"
    fi
    tar -xvf openshift-client-linux.tar.gz
    sudo mv oc kubectl /usr/local/bin/
    oc version || true
fi

# Install Go
GO_VERSION=$(curl -s https://go.dev/VERSION?m=text | head -n 1)

echo "Installing Go $GO_VERSION for $GOARCH"

curl -Lo ./go.tar.gz "https://go.dev/dl/${GO_VERSION}.linux-${GOARCH}.tar.gz"

INSTALL_DIR="/usr/local"
sudo rm -rf "${INSTALL_DIR}/go"
sudo tar -C "${INSTALL_DIR}" -xzf ./go.tar.gz

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
go get github.com/onsi/gomega/...

# Install kind
curl -Lo ./kind "https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-${GOARCH}"
chmod +x ./kind
sudo mv ./kind /usr/bin/kind

# Increase inotify limits for kind
sudo sysctl -w fs.inotify.max_user_instances=512
sudo sysctl -w fs.inotify.max_user_watches=524288
