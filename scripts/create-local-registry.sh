#!/bin/bash
set -x
set -euo pipefail

VM_IP=$1

mkdir -p ~/registry

# Generate the private key/cert
openssl req -x509 -newkey rsa:4096 -sha256 -days 3650 -nodes  -keyout ~/registry/registry.key -out ~/registry/registry.crt -subj "/CN=registry"  -addext "subjectAltName=DNS:registry,DNS:localhost,IP:$VM_IP"

openssl x509 -in ~/registry/registry.crt -out ~/registry/registry.pem -outform PEM
sudo mv ~/registry/registry.pem /etc/pki/ca-trust/source/anchors/
update-ca-trust

podman run -d \
  --restart=always \
  --name registry \
  --replace \
  -v ~/registry:/certs:Z \
  -e REGISTRY_HTTP_ADDR=0.0.0.0:443 \
  -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt \
  -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
  -p 443:443 \
  registry:2

