PACKAGE=github.com/openshift/ptp-operator
MAIN_PACKAGE=$(PACKAGE)/cmd/manager

BIN=build/_output/bin/ptp-operator
GOOS=linux
GO_BUILD=GOOS=$(GOOS) go build -o $(BIN) $(MAIN_PACKAGE)

all: buildbin

buildbin:
	$(GO_BUILD)

clean:
	rm -rf build/_output/bin/ptp-operator
