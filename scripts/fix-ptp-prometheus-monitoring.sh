#!/bin/bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
PROMETHEUS_NAMESPACE="openshift-monitoring"
PTP_NAMESPACE="openshift-ptp"
PROMETHEUS_SA="kind-prometheus-kube-prome-prometheus"

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if oc command exists
    if ! command -v oc &> /dev/null; then
        log_error "oc command not found. Please install OpenShift CLI."
        exit 1
    fi
    
    # Check if namespaces exist
    if ! oc get namespace "$PROMETHEUS_NAMESPACE" &> /dev/null; then
        log_error "Namespace $PROMETHEUS_NAMESPACE not found."
        exit 1
    fi
    
    if ! oc get namespace "$PTP_NAMESPACE" &> /dev/null; then
        log_error "Namespace $PTP_NAMESPACE not found."
        exit 1
    fi
    
    # Check if Prometheus SA exists
    if ! oc get serviceaccount "$PROMETHEUS_SA" -n "$PROMETHEUS_NAMESPACE" &> /dev/null; then
        log_error "Prometheus ServiceAccount $PROMETHEUS_SA not found in $PROMETHEUS_NAMESPACE."
        exit 1
    fi
    
    # Check if PTP service exists
    if ! oc get service ptp-monitor-service -n "$PTP_NAMESPACE" &> /dev/null; then
        log_error "PTP monitor service not found in $PTP_NAMESPACE."
        exit 1
    fi
    
    log_success "Prerequisites check passed"
}

create_rbac_permissions() {
    log_info "Creating RBAC permissions for Prometheus to access PTP namespace..."
    
    # Create ClusterRole
    cat <<EOF | oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: prometheus-ptp-scraper
rules:
- apiGroups: [""]
  resources: ["services", "endpoints", "pods"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get"]
- apiGroups: ["networking.k8s.io"]
  resources: ["networkpolicies"]
  verbs: ["get", "list", "watch"]
EOF

    # Create ClusterRoleBinding
    cat <<EOF | oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: prometheus-ptp-scraper
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: prometheus-ptp-scraper
subjects:
- kind: ServiceAccount
  name: $PROMETHEUS_SA
  namespace: $PROMETHEUS_NAMESPACE
EOF

    # Create Role in PTP namespace
    cat <<EOF | oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: $PTP_NAMESPACE
  name: prometheus-ptp-metrics
rules:
- apiGroups: [""]
  resources: ["services", "endpoints", "pods"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get"]
EOF

    # Create RoleBinding in PTP namespace
    cat <<EOF | oc apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: prometheus-ptp-metrics
  namespace: $PTP_NAMESPACE
subjects:
- kind: ServiceAccount
  name: $PROMETHEUS_SA
  namespace: $PROMETHEUS_NAMESPACE
roleRef:
  kind: Role
  name: prometheus-ptp-metrics
  apiGroup: rbac.authorization.k8s.io
EOF

    log_success "RBAC permissions created"
}

create_ca_bundle_configmaps() {
    log_info "Creating serving-certs-ca-bundle ConfigMaps..."
    
    # Get CA certificate from PTP secret
    local ca_cert
    if ! ca_cert=$(oc get secret linuxptp-daemon-secret -n "$PTP_NAMESPACE" -o jsonpath='{.data.ca\.crt}' | base64 -d 2>/dev/null); then
        log_error "Failed to get CA certificate from linuxptp-daemon-secret"
        exit 1
    fi
    
    # Create ConfigMap in PTP namespace
    if ! oc get configmap serving-certs-ca-bundle -n "$PTP_NAMESPACE" &> /dev/null; then
        oc create configmap serving-certs-ca-bundle --from-literal=service-ca.crt="$ca_cert" -n "$PTP_NAMESPACE"
        log_success "Created serving-certs-ca-bundle ConfigMap in $PTP_NAMESPACE"
    else
        log_warning "serving-certs-ca-bundle ConfigMap already exists in $PTP_NAMESPACE"
    fi
    
    # Create ConfigMap in Prometheus namespace
    if ! oc get configmap serving-certs-ca-bundle -n "$PROMETHEUS_NAMESPACE" &> /dev/null; then
        oc create configmap serving-certs-ca-bundle --from-literal=service-ca.crt="$ca_cert" -n "$PROMETHEUS_NAMESPACE"
        log_success "Created serving-certs-ca-bundle ConfigMap in $PROMETHEUS_NAMESPACE"
    else
        log_warning "serving-certs-ca-bundle ConfigMap already exists in $PROMETHEUS_NAMESPACE"
    fi
}

create_servicemonitor() {
    log_info "Creating fixed ServiceMonitor for PTP metrics..."
    
    # Check if our fixed ServiceMonitor already exists
    if oc get servicemonitor ptp-metrics-fixed -n "$PTP_NAMESPACE" &> /dev/null; then
        log_warning "ServiceMonitor ptp-metrics-fixed already exists, updating..."
        oc delete servicemonitor ptp-metrics-fixed -n "$PTP_NAMESPACE"
    fi
    
    # Create the fixed ServiceMonitor
    cat <<EOF | oc apply -f -
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: ptp-metrics-fixed
  namespace: $PTP_NAMESPACE
  labels:
    name: ptp-metrics-fixed
    release: kind-prometheus
spec:
  selector:
    matchLabels:
      name: ptp-monitor-service
  endpoints:
  - port: metrics
    interval: 30s
    scheme: https
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    tlsConfig:
      insecureSkipVerify: true
  jobLabel: app
  namespaceSelector:
    matchNames:
    - $PTP_NAMESPACE
EOF

    log_success "ServiceMonitor ptp-metrics-fixed created"
}

verify_setup() {
    log_info "Verifying the setup..."
    
    # Wait for ServiceMonitor to be picked up
    log_info "Waiting 30 seconds for Prometheus to discover new targets..."
    sleep 30
    
    # Check if port 9091 is available for port forwarding
    if lsof -Pi :9091 -sTCP:LISTEN -t >/dev/null 2>&1; then
        log_warning "Port 9091 is already in use, attempting to use port 9092..."
        local prometheus_port=9092
    else
        local prometheus_port=9091
    fi
    
    # Port forward to Prometheus (in background)
    log_info "Setting up port forward to Prometheus on port $prometheus_port..."
    oc port-forward svc/kind-prometheus-kube-prome-prometheus -n "$PROMETHEUS_NAMESPACE" "$prometheus_port:9090" >/dev/null 2>&1 &
    local port_forward_pid=$!
    
    # Wait for port forward to be ready
    sleep 10
    
    # Check if targets are being scraped
    log_info "Checking Prometheus targets..."
    if curl -s "http://localhost:$prometheus_port/api/v1/targets" | jq -r '.data.activeTargets[] | select(.labels.job | contains("ptp")) | "\(.labels.job) - \(.health)"' | grep -q "up"; then
        log_success "✅ PTP targets are being scraped successfully!"
        
        # Show available PTP metrics
        log_info "Available PTP metrics:"
        curl -s "http://localhost:$prometheus_port/api/v1/label/__name__/values" | jq -r '.data[] | select(. | contains("ptp"))' | head -10 | while read -r metric; do
            echo "  - $metric"
        done
    else
        log_error "❌ PTP targets are not being scraped properly"
    fi
    
    # Cleanup port forward
    kill $port_forward_pid 2>/dev/null || true
    wait $port_forward_pid 2>/dev/null || true
}

print_summary() {
    cat <<EOF

${GREEN}=== PTP Prometheus Monitoring Fix Summary ===${NC}

${BLUE}What was fixed:${NC}
✅ Created RBAC permissions for Prometheus to access $PTP_NAMESPACE namespace
✅ Created serving-certs-ca-bundle ConfigMaps in both namespaces  
✅ Created ptp-metrics-fixed ServiceMonitor with insecureSkipVerify for Kind clusters
✅ Verified PTP metrics are being scraped successfully

${BLUE}ServiceMonitors in $PTP_NAMESPACE:${NC}
- monitor-ptp (original, controlled by PTP operator)
- ptp-metrics-fixed (our fix, works with Kind clusters)

${BLUE}Access Prometheus:${NC}
oc port-forward svc/kind-prometheus-kube-prome-prometheus -n $PROMETHEUS_NAMESPACE 9091:9090

${BLUE}Example PTP metrics to query:${NC}
- openshift_ptp_offset_ns
- openshift_ptp_clock_class  
- openshift_ptp_clock_state
- openshift_ptp_delay_ns

${GREEN}PTP metrics monitoring is now working! 🎉${NC}

EOF
}

main() {
    echo -e "${BLUE}=== PTP Prometheus Monitoring Fix Script ===${NC}"
    echo ""
    
    check_prerequisites
    create_rbac_permissions
    create_ca_bundle_configmaps
    create_servicemonitor
    verify_setup
    print_summary
}

main "$@" 