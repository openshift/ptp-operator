module github.com/openshift/linuxptp-daemon

go 1.16

require (
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/jaypipes/ghw v0.0.0-20190630182512-29869ac89830
	github.com/openshift/ptp-operator v0.0.0-20211118144821-2c782f302ffb
	github.com/prometheus/client_golang v1.11.0
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v1.5.2
)

// Manually pinned to kubernetes-1.22.2
replace (
	k8s.io/api => k8s.io/api v0.22.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.22.2
	k8s.io/client-go => k8s.io/client-go v0.22.2
)

replace github.com/operator-framework/operator-sdk => github.com/operator-framework/operator-sdk v1.13.0
