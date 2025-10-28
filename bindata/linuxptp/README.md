# PTP Event Publisher Authentication Guide

Technical implementation guide for PTP Event Publisher authentication using OpenShift Service CA and OAuth server.

## Authentication Components

The service uses:
1. **mTLS**: OpenShift Service CA for transport security
2. **OAuth**: Service Account tokens with OpenShift OAuth server validation

## Components

### Service CA Configuration

Each PTP Event Publisher instance has its own:
- Service CA-managed certificates
- CA bundle for client verification
- Node-specific service and configuration

```yaml
# Service with certificate generation
apiVersion: v1
kind: Service
metadata:
  name: ptp-event-publisher-service-<node-name>
  annotations:
    service.beta.openshift.io/serving-cert-secret-name: ptp-event-publisher-certs-<node-name>

# CA bundle ConfigMap
apiVersion: v1
kind: ConfigMap
metadata:
  name: ptp-event-publisher-ca-bundle-<node-name>
  annotations:
    service.beta.openshift.io/inject-cabundle: "true"
```

### Authentication Configuration

The authentication settings are stored in a ConfigMap with **strict OAuth validation**:
```json
{
  "enableMTLS": true,
  "useServiceCA": true,
  "caCertPath": "/etc/cloud-event-proxy/ca-bundle/tls.crt",
  "serverCertPath": "/etc/cloud-event-proxy/certs/tls.crt",
  "serverKeyPath": "/etc/cloud-event-proxy/certs/tls.key",
  "enableOAuth": true,
  "useOpenShiftOAuth": true,
  "oauthIssuer": "https://oauth-openshift.apps.{{.ClusterName}}",
  "oauthJWKSURL": "https://oauth-openshift.apps.{{.ClusterName}}/oauth/jwks",
  "requiredScopes": ["user:info"],
  "requiredAudience": "openshift",
  "serviceAccountName": "cloud-event-proxy-sa",
  "serviceAccountToken": "/var/run/secrets/kubernetes.io/serviceaccount/token"
}
```

### OAuth Configuration

- OAuth URLs automatically generated from `CLUSTER_NAME` environment variable
- Strict token validation: issuer, expiration, audience, and signature
- Service Account token authentication

### Container Configuration

The cloud-event-proxy container is configured with:
- Volume mounts for certificates and configuration
- Authentication configuration argument
- HTTPS health check in readiness probe

```yaml
volumeMounts:
  - name: server-certs
    mountPath: /etc/cloud-event-proxy/certs
  - name: ca-bundle
    mountPath: /etc/cloud-event-proxy/ca-bundle
  - name: auth-config
    mountPath: /etc/cloud-event-proxy/auth
```

## Cluster Configuration

### Cluster Name Configuration

The `CLUSTER_NAME` environment variable must match your OpenShift cluster domain for OAuth authentication to work.

**Setting the cluster name during deployment:**

```bash
# For manual deployment (must match your actual cluster domain)
export CLUSTER_NAME="your-cluster.example.com"

# For OLM deployment, update the ClusterServiceVersion:
oc patch csv ptp-operator.v4.21.0 -n openshift-ptp --type='json' \
  -p='[{"op": "add", "path": "/spec/install/spec/deployments/0/spec/template/spec/containers/0/env/-", "value": {"name": "CLUSTER_NAME", "value": "your-cluster.example.com"}}]'

# Verify configuration
oc get deployment ptp-operator -n openshift-ptp -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="CLUSTER_NAME")].value}'
```

**Default behavior:**
- If `CLUSTER_NAME` is not set, defaults to `openshift.local`
- OAuth server URL will be: `https://oauth-openshift.apps.${CLUSTER_NAME}`
- OAuth issuer validation: Tokens must have issuer matching the OAuth server URL exactly
- Cluster info is stored in the `cluster-info` ConfigMap for use by the linuxptp-daemon pods

## Security Features

1. **mTLS**: Per-node certificates with automatic Service CA rotation
2. **OAuth**: OpenShift OAuth server with Service Account token validation
3. **TLS**: TLS 1.2+ with client certificate validation

## Client Configuration

Clients connecting to the PTP Event Publisher must:
1. Mount the CA bundle from `ptp-event-publisher-ca-bundle-<node-name>`
2. Use a Service Account with appropriate RBAC permissions
3. Present the Service Account token in requests

Example client configuration:
```yaml
spec:
  containers:
    - name: event-consumer
      volumeMounts:
        - name: service-ca
          mountPath: /etc/cloud-event-consumer/service-ca
      env:
        - name: SERVICE_ACCOUNT_TOKEN
          valueFrom:
            secretKeyRef:
              name: $(SERVICE_ACCOUNT_NAME)-token
              key: token
  volumes:
    - name: service-ca
      configMap:
        name: ptp-event-publisher-ca-bundle-<node-name>
```

## Troubleshooting

### Certificate Issues
```bash
# Check service CA certificate
oc get configmap ptp-event-publisher-ca-bundle-<node-name> -n openshift-ptp

# Check server certificate
oc get secret ptp-event-publisher-certs-<node-name> -n openshift-ptp
```

### Authentication Issues

#### General Authentication
```bash
# Check service account
oc get serviceaccount <service-account-name> -n <namespace>

# Check RBAC
oc get rolebinding -n openshift-ptp

# Check pod logs
oc logs ds/linuxptp-daemon -c cloud-event-proxy -n openshift-ptp
```

#### OAuth-Specific Issues

**Common OAuth Error Messages:**
- `Token issuer mismatch: expected https://oauth-openshift.apps.cluster.com, got https://dummy.com`
- `Token expired`
- `Authorization header required`
- `Bearer token required`
- `Invalid OAuth token`

**Troubleshooting OAuth Issues:**
```bash
# Check cluster name configuration
oc get deployment ptp-operator -n openshift-ptp -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="CLUSTER_NAME")].value}'

# Verify OAuth server accessibility
CLUSTER_NAME=$(oc get deployment ptp-operator -n openshift-ptp -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="CLUSTER_NAME")].value}')
curl -k "https://oauth-openshift.apps.${CLUSTER_NAME:-openshift.local}/oauth/jwks"

# Check authentication configuration in ConfigMap
oc get configmap ptp-event-publisher-auth -n openshift-ptp -o jsonpath='{.data.config\.json}' | jq .

# Verify OAuth issuer matches cluster configuration
echo "Expected OAuth Issuer: https://oauth-openshift.apps.${CLUSTER_NAME:-openshift.local}"
```

#### Updating Cluster Name at Runtime

If you need to update the cluster name after deployment, follow these steps:

**Step 1: Update the CLUSTER_NAME environment variable**
```bash
# Update the operator deployment with your actual cluster domain
oc set env deployment/ptp-operator -n openshift-ptp CLUSTER_NAME=your-actual-cluster.example.com

# Wait for the deployment to restart
oc rollout status deployment/ptp-operator -n openshift-ptp
```

**Step 2: Trigger reconciliation (if needed)**
```bash
# Force the operator to reconcile all authentication resources
oc patch ptpoperatorconfig default -n openshift-ptp --type='merge' -p '{"metadata":{"annotations":{"force-reconcile":"'$(date +%s)'"}}}'
```

**Step 3: Verify the updates**
```bash
# Check if cluster-info ConfigMap has been updated
oc get configmap cluster-info -n openshift-ptp -o jsonpath='{.data.cluster-name}'

# Verify OAuth URLs in authentication configuration
oc get configmap ptp-event-publisher-auth -n openshift-ptp -o jsonpath='{.data.config\.json}' | jq '.oauthIssuer'

# Check all authentication resources
oc get configmap,service -n openshift-ptp | grep -E "cluster-info|ptp-event-publisher|cloud-event-proxy"
```

**Example: Updating to a real cluster**
```bash
# Update cluster name to actual domain
oc set env deployment/ptp-operator -n openshift-ptp CLUSTER_NAME=cnfdg4.sno.ptp.eng.rdu2.dc.redhat.com

# Wait for restart
oc rollout status deployment/ptp-operator -n openshift-ptp

# Verify the cluster-info ConfigMap shows the correct name
oc get configmap cluster-info -n openshift-ptp -o jsonpath='{.data.cluster-name}'
# Should output: cnfdg4.sno.ptp.eng.rdu2.dc.redhat.com

# Verify OAuth URLs are updated
oc get configmap ptp-event-publisher-auth -n openshift-ptp -o jsonpath='{.data.config\.json}' | jq '.oauthIssuer'
# Should output: "https://oauth-openshift.apps.cnfdg4.sno.ptp.eng.rdu2.dc.redhat.com"
```

**Important Notes:**
- The operator automatically updates all authentication ConfigMaps when `CLUSTER_NAME` is changed
- OAuth URLs are dynamically generated based on the cluster name
- Changes take effect after the operator pod restarts (usually within 1-2 minutes)
- All existing authentication resources are updated automatically

### Health Check
```bash
# Test health endpoint with certificate
curl -s https://ptp-event-publisher-service-<node-name>.openshift-ptp.svc:9043/health \
  --cacert /path/to/service-ca.crt
```

## Security Considerations

1. **Certificate Management**
   - Certificates are automatically rotated
   - Private keys are stored in Secrets
   - CA bundle is updated automatically

2. **Network Security**
   - All traffic is encrypted
   - Client certificates required
   - Token validation for every request

3. **Access Control**
   - RBAC for service access
   - Scoped permissions
   - Audit logging enabled
