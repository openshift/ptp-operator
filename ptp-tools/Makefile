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
SAVE_DIR ?= /tmp/ptp-images

VALUES = lptpd cep ptpop krp openvswitch prometheus ptpmg debug

podman-buildall: $(foreach v, $(VALUES), podman-build-$(v))

podman-build-%:
	@echo "Platform: $(PLATFORM)"
	$(MAKE) clean-image VAR=$*
	$(MAKE) build-image VAR=$*

podman-pushall: $(foreach v, $(VALUES), podman-push-$(v))

podman-push-%:
	podman manifest push ${IMG_PREFIX}:$* docker://${IMG_PREFIX}:$*

podman-saveall: $(foreach v, $(VALUES), podman-save-$(v))

podman-save-%:
	mkdir -p $(SAVE_DIR)
	podman save -m -o $(SAVE_DIR)/$*.tar ${IMG_PREFIX}:$*

podman-build-and-pushall: $(foreach v, $(VALUES), podman-build-and-push-$(v))

podman-build-and-push-%:
	$(MAKE) podman-clean-$*
	$(MAKE) podman-build-$*
	$(MAKE) podman-push-$*

podman-build-and-saveall: $(foreach v, $(VALUES), podman-build-and-save-$(v))

podman-build-and-save-%:
	$(MAKE) podman-clean-$*
	$(MAKE) podman-build-$*
	$(MAKE) podman-save-$*

build-image:
	podman manifest create ${IMG_PREFIX}:$(VAR)
	podman build --no-cache --network host --platform $(PLATFORM) -f Dockerfile.$(VAR)  --manifest ${IMG_PREFIX}:$(VAR)  ..

clean-image:
	podman manifest rm ${IMG_PREFIX}:$(VAR) || true

podman-clean-%:
	podman manifest rm ${IMG_PREFIX}:$* || true

podman-cleanall: $(foreach v, $(VALUES), podman-clean-$(v))

.PHONY: podman-buildall podman-pushall podman-saveall podman-cleanall
.PHONY: podman-build-and-pushall podman-build-and-saveall
.PHONY: build-image clean-image deploy-all

deploy-all: 
	./scripts/customize-env.sh ${IMG_PREFIX} ../config/manager
	cd ../config/manager && $(KUSTOMIZE) edit set image controller=${IMG_PREFIX}:ptpop
	$(KUSTOMIZE) build ../config/default | kubectl apply -f -
	$(KUSTOMIZE) build ../config/custom | kubectl apply -f -
	kubectl patch ptpoperatorconfig default -nopenshift-ptp --type=merge --patch '{"spec": {"ptpEventConfig": {"enableEventPublisher": true, "transportHost": "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043", "storageType": "local-sc"}, "daemonNodeSelector": {"node-role.kubernetes.io/worker": ""}}}'
