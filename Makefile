PACKAGE=github.com/openshift/ptp-operator
MAIN_PACKAGE=$(PACKAGE)/cmd/manager

BIN=build/_output/bin/ptp-operator
GOOS=linux
GO_BUILD=GOOS=$(GOOS) go build -o $(BIN) $(MAIN_PACKAGE)

all: buildbin

buildbin:
	$(GO_BUILD)

image:
	docker build -t openshift.io/ptp-operator -f Dockerfile.rhel7 .

clean:
	rm -rf build/_output/bin/ptp-operator
