# PTP Event Publisher Authentication

This document describes the authentication setup for the PTP Event Publisher service in the linuxptp-daemon.

## Overview

The PTP Event Publisher service uses two authentication mechanisms:
1. mTLS (Mutual TLS) for transport security
2. OAuth with Service Account tokens for client authentication

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

The authentication settings are stored in a node-specific ConfigMap:
```json
{
  "enableMTLS": true,
  "caCertPath": "/etc/cloud-event-proxy/ca-bundle/service-ca.crt",
  "serverCertPath": "/etc/cloud-event-proxy/certs/tls.crt",
  "serverKeyPath": "/etc/cloud-event-proxy/certs/tls.key",
  "enableOAuth": true,
  "oauthIssuer": "https://kubernetes.default.svc",
  "oauthJWKSURL": "https://kubernetes.default.svc/.well-known/openid-configuration",
  "requiredScopes": ["cloud-event-proxy"],
  "requiredAudience": "cloud-event-proxy"
}
```

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

## Security Features

1. **Per-Node Certificates**
   - Each node has its own certificate
   - Automatic rotation by Service CA operator
   - Secure storage in Secrets

2. **OAuth Integration**
   - Uses OpenShift's OAuth server
   - Service Account token authentication
   - Scope and audience validation

3. **TLS Configuration**
   - TLS 1.2 or higher
   - Client certificate validation
   - Secure cipher suites

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
```bash
# Check service account
oc get serviceaccount <service-account-name> -n <namespace>

# Check RBAC
oc get rolebinding -n openshift-ptp

# Check pod logs
oc logs ds/linuxptp-daemon -c cloud-event-proxy -n openshift-ptp
```

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
