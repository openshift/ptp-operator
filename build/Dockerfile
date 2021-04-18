FROM registry.access.redhat.com/ubi7/ubi-minimal:latest

ENV OPERATOR=/usr/local/bin/ptp-operator \
    USER_UID=1001 \
    USER_NAME=ptp-operator

# install operator binary
COPY build/_output/bin/ptp-operator ${OPERATOR}

COPY build/bin /usr/local/bin
COPY bindata /bindata
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
