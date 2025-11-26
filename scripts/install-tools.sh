#!/bin/bash
set -x
set -euo pipefail

# Install tools required for testing
yum install -y podman pciutils helm

ARCH=$(uname -m)
if [[ "$ARCH" == "x86_64" ]]; then
    GOARCH="amd64"
elif [[ "$ARCH" == "aarch64" ]]; then
    GOARCH="arm64"
fi
if [[ "$ARCH" != "x86_64" && "$ARCH" != "aarch64" ]]; then
    echo "Unsupported operating system $(uname)."
fi
# Install kubectl
echo "Installing kubectl/oc for $ARCH"
if [[ "$ARCH" == "x86_64" ]]; then
    curl -Lo ./openshift-client-linux.tar.gz "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux.tar.gz"
elif [[ "$ARCH" == "aarch64" ]]; then
    curl -Lo openshift-client-linux.tar.gz "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux-arm64.tar.gz"
fi
tar -xvf openshift-client-linux.tar.gz
sudo mv oc kubectl /usr/local/bin/
oc version || true


# Install go
GO_VERSION=$(curl -s https://go.dev/VERSION?m=text | head -n 1)

echo "Installing Go $GO_VERSION for $GOARCH"

curl -Lo ./go.tar.gz https://go.dev/dl/${GO_VERSION}.linux-${GOARCH}.tar.gz

INSTALL_DIR="/usr/local"
sudo rm -rf "${INSTALL_DIR}/go"
sudo tar -C "${INSTALL_DIR}" -xzf ./go.tar.gz

# Add to profile if not already added
if ! grep -q 'export PATH=$PATH:"$HOME"/go/bin:/usr/local/go/bin' ~/.bashrc; then
    echo 'export PATH=$PATH:"$HOME"/go/bin:/usr/local/go/bin' >>~/.bashrc
    echo "Go path added to ~/.bashrc. Run 'source ~/.bashrc' or restart your shell."
fi

export BASHRCSOURCED=1
source ~/.bashrc

# Install ginkgo
go mod tidy
go mod vendor
go install github.com/onsi/ginkgo/v2/ginkgo
go get github.com/onsi/gomega/...

# Install kind
if [[ "$ARCH" == "x86_64" ]]; then
    curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-amd64
elif [[ "$ARCH" == "aarch64" ]]; then
    curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-arm64
else
    echo "Unsupported operating system $(uname)."
    exit 1
fi
chmod +x ./kind
sudo mv ./kind /usr/bin/kind

# increase watches and instances
echo fs.inotify.max_user_instances=512 | sudo tee -a /etc/sysctl.conf
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p
