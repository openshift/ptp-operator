#!/bin/bash
curl -LO "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/openshift-client-linux.tar.gz"
tar -xvf openshift-client-linux.tar.gz
sudo mv oc kubectl /usr/local/bin/
oc version

# Install gvm
bash < <(curl -s -S -L https://raw.githubusercontent.com/moovweb/gvm/master/binscripts/gvm-installer)
source /root/.gvm/scripts/gvm
gvm install go1.23.0 -B
gvm use go1.23.0


# Install ginkgo
go install github.com/onsi/ginkgo/v2/ginkgo
go get github.com/onsi/gomega/...

# Install kind
[ $(uname -m) = x86_64 ] && curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.27.0/kind-linux-amd64
chmod +x ./kind
sudo mv ./kind /usr/bin/kind

# increase watches and instances
echo fs.inotify.max_user_instances=512 | sudo tee -a /etc/sysctl.conf
echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf
sudo sysctl -p