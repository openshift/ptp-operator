FROM golang:1.19 AS builder
WORKDIR /go/src/github.com/openshift/linuxptp-daemon
COPY . .
RUN make clean && make

FROM quay.io/openshift/origin-base:4.13
RUN yum -y update && yum --setopt=skip_missing_names_on_install=False -y install linuxptp ethtool hwdata && yum clean all
COPY --from=builder /go/src/github.com/openshift/linuxptp-daemon/bin/ptp /usr/local/bin/
COPY ./extra/leap-seconds.list /usr/share/zoneinfo/leap-seconds.list

CMD ["/usr/local/bin/ptp"]
