FROM fedora:30 AS builder

RUN yum install -y make golang

RUN mkdir /usr/src/linuxptp-daemon
WORKDIR /usr/src/linuxptp-daemon

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN make clean && go build -tags no_openssl -o bin/ptp ./cmd

FROM fedora:30
RUN yum install -y linuxptp ethtool make hwdata
COPY --from=builder /usr/src/linuxptp-daemon/bin/ptp /usr/local/bin/ptp

CMD ["/usr/local/bin/ptp"]
