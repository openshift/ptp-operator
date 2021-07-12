FROM fedora:32 AS builder

RUN yum install -y make golang

ENV GOPATH="/go"
WORKDIR /go/src/github.com/openshift/ptp-operator

COPY . .
RUN make daemon-clean && make daemon-build

FROM fedora:30
RUN yum install -y linuxptp ethtool make hwdata
COPY --from=builder /go/src/github.com/openshift/ptp-operator/bin/ptp /usr/local/bin/ptp

CMD ["/usr/local/bin/ptp"]
