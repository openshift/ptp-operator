#!/bin/bash
set -euo pipefail
kubectl get secret webhook-server-cert -n openshift-ptp -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt
kubectl patch validatingwebhookconfiguration ptpconfig-validating-webhook-configuration --type='json' -p="[{'op': 'replace', 'path': '/webhooks/0/clientConfig/caBundle', 'value': '$(cat ca.crt | base64 -w0)'}]"
kubectl patch validatingwebhookconfiguration ptpconfig-validating-webhook-configuration --type='json' -p="[{'op': 'replace', 'path': '/webhooks/1/clientConfig/caBundle', 'value': '$(cat ca.crt | base64 -w0)'}]"
