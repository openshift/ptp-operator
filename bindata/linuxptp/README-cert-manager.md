# PTP Operator Authentication with cert-manager and OpenShift Authentication Operator

This document describes how the PTP Operator integrates with OpenShift's cert-manager operator for mTLS certificate management and OpenShift's Authentication operator for OAuth authentication.

## Overview

The PTP Operator now supports enterprise-grade authentication using:

1. **cert-manager Operator**: For automatic mTLS certificate generation and management
2. **OpenShift Authentication Operator**: For OAuth token validation and user authentication

## Prerequisites

### cert-manager Operator
- cert-manager operator must be installed in the cluster
- A ClusterIssuer must be configured (e.g., `openshift-cluster-issuer`)
- The ClusterIssuer should be configured to work with your certificate authority

### OpenShift Authentication Operator
- OpenShift cluster with authentication operator enabled
- ServiceAccount with appropriate RBAC permissions
- OAuth server accessible within the cluster

## Configuration

### 1. cert-manager Certificate Resources

The PTP Operator creates cert-manager Certificate resources for mTLS:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: cloud-event-proxy-mtls-{{.NodeName}}
  namespace: openshift-ptp
spec:
  secretName: cloud-event-proxy-mtls-tls-{{.NodeName}}
  issuerRef:
    name: openshift-cluster-issuer
    kind: ClusterIssuer
  dnsNames:
  - cloud-event-proxy.openshift-ptp.svc.cluster.local
  - cloud-event-proxy.openshift-ptp.svc
  - cloud-event-proxy
  - {{.NodeName}}.openshift-ptp.svc.cluster.local
  usages:
  - digital signature
  - key encipherment
  - client auth
  - server auth
```

### 2. Authentication Configuration

The authentication configuration is stored in a ConfigMap:

```json
{
  "enableMTLS": true,
  "caCertPath": "/etc/cloud-event-proxy/ca-bundle/ca.crt",
  "serverCertPath": "/etc/cloud-event-proxy/server-certs/tls.crt",
  "serverKeyPath": "/etc/cloud-event-proxy/server-certs/tls.key",
  "certManagerIssuer": "openshift-cluster-issuer",
  "certManagerNamespace": "openshift-ptp",
  "enableOAuth": true,
  "oauthIssuer": "https://oauth-openshift.apps.openshift.example.com",
  "oauthJWKSURL": "https://oauth-openshift.apps.openshift.example.com/.well-known/openid_configuration",
  "requiredScopes": ["user:info", "user:check-access"],
  "requiredAudience": "openshift",
  "serviceAccountName": "cloud-event-proxy-client",
  "serviceAccountToken": "/var/run/secrets/kubernetes.io/serviceaccount/token",
  "authenticationOperator": true
}
```

### 3. RBAC Configuration

The PTP Operator creates appropriate RBAC resources:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: openshift-ptp
  name: cloud-event-proxy-oauth-{{.NodeName}}
rules:
- apiGroups: [""]
  resources: ["serviceaccounts"]
  verbs: ["get", "list"]
- apiGroups: ["authentication.k8s.io"]
  resources: ["tokenreviews"]
  verbs: ["create"]
- apiGroups: ["authorization.k8s.io"]
  resources: ["subjectaccessreviews"]
  verbs: ["create"]
- apiGroups: ["user.openshift.io"]
  resources: ["users", "groups"]
  verbs: ["get", "list"]
```

## Deployment

### 1. Install cert-manager Operator

```bash
# Install cert-manager operator from OperatorHub
oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: cert-manager
  namespace: openshift-operators
spec:
  channel: stable
  name: cert-manager
  source: redhat-operators
  sourceNamespace: openshift-marketplace
EOF
```

### 2. Configure ClusterIssuer

```bash
# Create a ClusterIssuer (example with Let's Encrypt)
oc apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: openshift-cluster-issuer
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@example.com
    privateKeySecretRef:
      name: openshift-cluster-issuer-account-key
    solvers:
    - http01:
        ingress:
          class: nginx
EOF
```

### 3. Deploy PTP Operator with Authentication

```bash
# Deploy the PTP Operator with authentication enabled
oc apply -f ptp-operator/bindata/linuxptp/auth-config-cert-manager.yaml
oc apply -f ptp-operator/bindata/linuxptp/ptp-daemon.yaml
```

## Certificate Management

### Automatic Certificate Generation

cert-manager automatically:
- Generates certificates based on the Certificate resource
- Stores certificates and keys in Kubernetes secrets
- Renews certificates before expiration
- Validates DNS names and certificate usage

### Certificate Monitoring

Monitor certificate status:

```bash
# Check certificate status
oc get certificates -n openshift-ptp

# Check certificate details
oc describe certificate cloud-event-proxy-mtls-<node-name> -n openshift-ptp

# Check certificate events
oc get events -n openshift-ptp --field-selector involvedObject.name=cloud-event-proxy-mtls-<node-name>
```

## OAuth Authentication

### ServiceAccount Token Authentication

The PTP Operator uses ServiceAccount tokens for OAuth authentication:

1. **Token Generation**: OpenShift automatically generates tokens for ServiceAccounts
2. **Token Validation**: Tokens are validated against OpenShift's OAuth server
3. **RBAC Integration**: Tokens are checked against RBAC rules

### Token Management

```bash
# Check ServiceAccount tokens
oc get serviceaccount cloud-event-proxy-client -n openshift-ptp -o yaml

# Check token secrets
oc get secrets -n openshift-ptp | grep cloud-event-proxy-client

# Test token authentication
oc auth can-i get pods --as=system:serviceaccount:openshift-ptp:cloud-event-proxy-client
```

## Troubleshooting

### Certificate Issues

1. **Certificate Not Ready**:
   ```bash
   oc describe certificate cloud-event-proxy-mtls-<node-name> -n openshift-ptp
   ```

2. **ClusterIssuer Issues**:
   ```bash
   oc describe clusterissuer openshift-cluster-issuer
   ```

3. **Certificate Order Issues**:
   ```bash
   oc get certificaterequests -n openshift-ptp
   ```

### OAuth Issues

1. **Token Validation Failures**:
   ```bash
   oc logs -n openshift-ptp -l app=linuxptp-daemon --tail=100
   ```

2. **RBAC Permission Issues**:
   ```bash
   oc auth can-i create tokenreviews --as=system:serviceaccount:openshift-ptp:cloud-event-proxy-<node-name>
   ```

3. **OAuth Server Connectivity**:
   ```bash
   oc get routes -n openshift-authentication
   ```

## Security Considerations

1. **Certificate Security**:
   - cert-manager automatically handles certificate rotation
   - Private keys are stored securely in Kubernetes secrets
   - Certificates are validated against DNS names

2. **OAuth Security**:
   - ServiceAccount tokens have limited scope
   - RBAC rules restrict access to necessary resources
   - Token validation uses OpenShift's built-in OAuth server

3. **Network Security**:
   - mTLS provides mutual authentication
   - All communication is encrypted
   - Certificate validation prevents man-in-the-middle attacks

## Migration from Service CA

If migrating from OpenShift Service CA to cert-manager:

1. **Update Configuration**: Change certificate paths and issuer references
2. **Deploy cert-manager Resources**: Apply the new Certificate resources
3. **Verify Certificates**: Ensure new certificates are generated and valid
4. **Update Applications**: Restart applications to use new certificates
5. **Remove Old Resources**: Clean up old Service CA resources

## Best Practices

1. **Certificate Management**:
   - Use appropriate DNS names in certificate requests
   - Monitor certificate expiration
   - Implement certificate rotation procedures

2. **OAuth Configuration**:
   - Use minimal required scopes
   - Implement proper RBAC rules
   - Monitor authentication logs

3. **Operational Procedures**:
   - Document certificate rotation procedures
   - Create incident response plans
   - Regular security audits
