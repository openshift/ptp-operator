FROM registry.access.redhat.com/ubi9/ubi:9.4-1214.1726694543
RUN dnf -y --disableplugin=subscription-manager install iputils iproute ethtool pciutils
RUN dnf -y --disableplugin=subscription-manager remove python3-setuptools
COPY l2discovery-linux-amd64 /usr/bin
COPY l2discovery-linux-arm64 /usr/bin
RUN \
	if [ "$(uname -m)" = x86_64 ]; then \
		echo "Detected x86_64 CPU architecture."; \
        mv /usr/bin/l2discovery-linux-amd64 /usr/bin/l2discovery; \
	elif [ "$(uname -m)" = aarch64 ]; then \
		echo "Detected aarch64 CPU architecture."; \
        mv /usr/bin/l2discovery-linux-arm64 /usr/bin/l2discovery; \
	else \
		echo "CPU architecture is not supported." && exit 1; \
	fi
USER 0
CMD ["/bin/sh", "-c", "/usr/bin/l2discovery"]

