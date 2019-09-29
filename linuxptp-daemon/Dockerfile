FROM fedora:30
ADD . /usr/src/linuxptp-daemon

WORKDIR /usr/src/linuxptp-daemon

RUN yum install -y linuxptp ethtool make hwdata golang
RUN make clean && make

WORKDIR /

CMD ["/usr/src/linuxptp-daemon/bin/ptp"]
