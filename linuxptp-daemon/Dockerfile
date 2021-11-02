FROM fedora:34 AS builder

RUN yum install -y make golang

ENV GOPATH="/go"
WORKDIR /go/src/github.com/openshift/linuxptp-daemon

COPY . .
RUN make clean && make

FROM fedora:33
RUN yum install -y linuxptp ethtool make hwdata
COPY --from=builder /go/src/github.com/openshift/linuxptp-daemon/bin/ptp /usr/local/bin/ptp

CMD ["/usr/local/bin/ptp"]
