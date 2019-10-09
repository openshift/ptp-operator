FROM fedora:30
ADD . /usr/src/linuxptp-daemon

WORKDIR /usr/src/linuxptp-daemon

RUN yum install -y linuxptp ethtool make hwdata golang
RUN make clean && make
RUN cp /usr/src/linuxptp-daemon/bin/ptp /usr/local/bin/ptp
WORKDIR /

CMD ["/usr/local/bin/ptp"]
