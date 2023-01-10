FROM golang:1.18 AS builder
WORKDIR /go/src/github.com/openshift/linuxptp-daemon
COPY . .
RUN make clean && make

FROM quay.io/openshift/origin-base:4.12
RUN yum -y update && yum --setopt=skip_missing_names_on_install=False -y install linuxptp ethtool hwdata && yum clean all
RUN wget https://www.ietf.org/timezones/data/leap-seconds.list -O /usr/share/zoneinfo/leap-seconds.list
COPY --from=builder /go/src/github.com/openshift/linuxptp-daemon/bin/ptp /usr/local/bin/

CMD ["/usr/local/bin/ptp"]
