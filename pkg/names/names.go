package names

import "os"

// Namespace is the namespace where resources are created,
// such as linuxptp daemonset, ptp-configmap-<node-name>
// and nodePtpDevice. It defaults to "openshift-ptp" but
// can be overridden via the OPERATOR_NAMESPACE env var
// to support OLMv1 AllNamespaces install mode.
var Namespace = "openshift-ptp"

func init() {
	if ns := os.Getenv("OPERATOR_NAMESPACE"); ns != "" {
		Namespace = ns
	}
}

// DefaultPTPConfigMapName is the default ptp config map that created
// by ptp-operator.
const DefaultPTPConfigMapName = "ptp-configmap"

// DefaultLeapConfigMapName is the default leap config map that created
// by ptp-operator.
const DefaultLeapConfigMapName = "leap-configmap"

// DefaultOperatorConfigName is the default operator config that
// created by ptp-operator. It's set to the owner of resources of
// linuxptp daemonset, ptp-configmap and nodePtpDevice.
const DefaultOperatorConfigName = "default"

// ManifestDir is the directory where manifests are located.
const ManifestDir = "./bindata"

// Event-publisher mTLS CA trust anchors.
//
// EventPublisherCABundleConfigMapName is the ConfigMap carrying the
// service.beta.openshift.io/inject-cabundle annotation. The OpenShift Service CA
// operator injects service-ca.crt into it; the ptp-operator maintains the
// derived combined trust file (EventPublisherCABundleKey) that cloud-event-proxy
// loads via caCertPath.
const EventPublisherCABundleConfigMapName = "ptp-event-publisher-ca-bundle"

// EventPublisherClientCAConfigMapName is an optional operator-read input holding
// an additional client CA to trust for mTLS (e.g. a test consumer's clientAuth
// CA that cannot be minted by the Service CA). Published out-of-band; when absent
// only Service CA-signed clients are trusted.
const EventPublisherClientCAConfigMapName = "ptp-event-publisher-client-ca"

// EventPublisherClientTLSSecretName is an optional out-of-band secret holding the
// clientAuth certificate (tls.crt/tls.key) that cloud-event-proxy presents when it
// acts as an mTLS *client* on the outbound push leg (delivering initial
// notifications to a consumer callback). The Service CA only mints serverAuth
// certificates, so the push client cannot reuse ptp-event-publisher-server-tls; a
// dedicated clientAuth cert must be supplied here. The daemonset mounts it
// optionally - when absent, outbound pushes rely on the ServiceAccount bearer
// token alone (see clientCertPath/clientKeyPath in auth-config.yaml).
const EventPublisherClientTLSSecretName = "ptp-event-publisher-client-tls"

// ServiceCAKey is the key the OpenShift Service CA operator injects into
// EventPublisherCABundleConfigMapName.
const ServiceCAKey = "service-ca.crt"

// EventPublisherCABundleKey is the operator-owned combined trust file
// (service-ca.crt ++ any client CA) referenced by cloud-event-proxy's caCertPath.
const EventPublisherCABundleKey = "ca-bundle.crt"
