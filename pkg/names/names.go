package names

// some names

// Namespace is the namespace where resources are created,
// such as linuxptp daemonset, ptp-configmap-<node-name>
// and nodePtpDevice.
const Namespace = "openshift-ptp"

// DefaultPTPConfigMapName is the default ptp config map that created
// by ptp-operator.
const DefaultPTPConfigMapName = "ptp-configmap"

// DefaultPTPLeapFileConfigMapName is the default ptp leap file config map that created
// by ptp-operator.
const DefaultPTPLeapFileConfigMapName = "ptp-leap-second-configmap"

// DefaultOperatorConfigName is the default operator config that
// created by ptp-operator. It's set to the owner of resources of
// linuxptp daemonset, ptp-configmap and nodePtpDevice.
const DefaultOperatorConfigName = "default"

// ManifestDir is the directory where manifests are located.
const ManifestDir = "./bindata"
