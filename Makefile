# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: bin

deps-update:
	go mod tidy && \
	go mod vendor

bin:
	hack/build.sh
image:
	docker build -t openshift.io/ptp-operator -f Dockerfile.rhel7 .
clean:
	rm -rf build/_output/bin/ptp-operator
deploy-setup:
	hack/deploy-setup.sh
undeploy:
	hack/undeploy.sh

functests:
	hack/run-functests.sh

generate-client:
	bash vendor/k8s.io/code-generator/generate-groups.sh client \
      	github.com/openshift/ptp-operator/pkg/client \
      	github.com/openshift/ptp-operator/pkg/apis \
      	ptp:v1

generate: operator-sdk
	$(GOBIN)/operator-sdk generate k8s
	$(GOBIN)/operator-sdk generate crds

# find or download controller-gen
# download controller-gen if necessary
operator-sdk:
	go install ./vendor/github.com/operator-framework/operator-sdk/cmd/operator-sdk
