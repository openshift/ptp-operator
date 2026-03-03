LOCALBIN ?= $(shell pwd)/../bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)/x86_64/

# Allow PLATFORM to be overridden via environment variable
# If not provided, auto-discover the platform
ifndef PLATFORM
OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
ARCH := $(shell uname -m)

# Normalize architecture names
ifeq ($(ARCH),x86_64)
  ARCH := amd64
endif
ifeq ($(ARCH),aarch64)
  ARCH := arm64
endif

PLATFORM := $(OS)/$(ARCH)
endif

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize

IMG_PREFIX ?= quay.io/<your user id here>/<your image name>

VALUES = lptpd cep ptpop krp openvswitch prometheus ptpmg

podman-buildall: $(foreach v, $(VALUES), podman-build-$(v))

podman-build-%:
	@echo "Platform: $(PLATFORM)"
	$(MAKE) clean-image VAR=$*;
	$(MAKE) build-image VAR=$*

podman-pushall: $(foreach v, $(VALUES), podman-push-$(v))

podman-push-%:
	podman manifest push ${IMG_PREFIX}:$*

build-image:
	podman manifest create ${IMG_PREFIX}:$(VAR)
	podman build --no-cache --platform $(PLATFORM) -f Dockerfile.$(VAR)  --manifest ${IMG_PREFIX}:$(VAR)  ..

clean-image:
	podman image rm ${IMG_PREFIX}:$(VAR) || true
	podman manifest rm ${IMG_PREFIX}:$(VAR) || true

podman-clean-%:
	podman manifest rm ${IMG_PREFIX}:$* || true

podman-cleanall: $(foreach v, $(VALUES), podman-clean-$(v))

deploy-all: 
	./scripts/customize-env.sh ${IMG_PREFIX} ../config/manager
	cd ../config/manager && $(KUSTOMIZE) edit set image controller=${IMG_PREFIX}:ptpop
	$(KUSTOMIZE) build ../config/default | kubectl apply -f -
	$(KUSTOMIZE) build ../config/custom | kubectl apply -f -
	kubectl patch ptpoperatorconfig default -nopenshift-ptp --type=merge --patch '{"spec": {"ptpEventConfig": {"enableEventPublisher": true, "transportHost": "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043", "storageType": "local-sc"}, "daemonNodeSelector": {"node-role.kubernetes.io/worker": ""}}}'
