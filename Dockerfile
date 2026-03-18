FROM registry.access.redhat.com/ubi9/go-toolset:1.25 AS builder
# Set as root to permission issues as its only for the builder
USER root
WORKDIR /go/src/github.com/k8snetworkplumbingwg/ptp-operator
COPY . .
ENV CGO_ENABLED=0
RUN make

FROM quay.io/centos/centos:stream9
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/ptp-operator/build/_output/bin/ptp-operator /usr/local/bin/
COPY --from=builder /go/src/github.com/k8snetworkplumbingwg/ptp-operator/manifests /manifests
COPY bindata /bindata

LABEL io.k8s.display-name="OpenShift ptp-operator" \
      io.k8s.description="This is a component that manages cluster PTP configuration." \
      io.openshift.tags="openshift,ptp" \
      com.redhat.delivery.appregistry=true \
      maintainer="PTP Team <ptp-dev@redhat.com>"

ENTRYPOINT ["/usr/local/bin/ptp-operator"]
