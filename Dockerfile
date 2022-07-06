FROM golang:1.17 AS builder
WORKDIR /go/src/github.com/openshift/ptp-operator
COPY . .
ENV GO111MODULE=off
RUN make

FROM quay.io/openshift/origin-base:4.12
COPY --from=builder /go/src/github.com/openshift/ptp-operator/build/_output/bin/ptp-operator /usr/local/bin/
COPY --from=builder /go/src/github.com/openshift/ptp-operator/manifests /manifests
COPY bindata /bindata

LABEL io.k8s.display-name="OpenShift ptp-operator" \
      io.k8s.description="This is a component that manages cluster PTP configuration." \
      io.openshift.tags="openshift,ptp" \
      com.redhat.delivery.appregistry=true \
      maintainer="Multus Team <multus-dev@redhat.com>"

ENTRYPOINT ["/usr/local/bin/ptp-operator"]
