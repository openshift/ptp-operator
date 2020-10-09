FROM registry.svc.ci.openshift.org/ocp/builder:rhel-8-golang-1.15-openshift-4.6 AS builder
WORKDIR /go/src/github.com/openshift/ptp-operator/must-gather
COPY . .

FROM registry.svc.ci.openshift.org/ocp/4.7:must-gather
COPY --from=builder /go/src/github.com/openshift/ptp-operator/must-gather/collection-scripts/* /usr/bin/
RUN chmod +x /usr/bin/gather

ENTRYPOINT /usr/bin/gather
