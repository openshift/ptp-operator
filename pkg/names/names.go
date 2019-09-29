package names

// some names

// Namespace is the namespace where resources are created,
// such as linuxptp daemonset, ptp-configmap-<node-name>
// and nodePtpDevice.
const Namespace = "ptp"

// DefaultCfgName is the default ptp config map that created
// by ptp-operator. It's set to the owner of resources of
// linuxptp daemonset, ptp-configmap-<node-name> and nodePtpDevice.
const DefaultCfgName = "default"

// ManifestDir is the directory where manifests are located.
const ManifestDir = "./bindata"
