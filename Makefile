PACKAGE=github.com/openshift/ptp-operator
MAIN_PACKAGE=$(PACKAGE)/cmd/manager

GOPATH=$(CURDIR)/.gopath
BASE=$(GOPATH)/src/$(PACKAGE)

BINDIR=$(CURDIR)/build/_output/bin
BINNAME=ptp-operator
GOOS=linux
GO_BUILD=GOOS=$(GOOS) go build -o $(BINDIR)/$(BINNAME)

all: buildbin

$(BASE): ; $(info  setting GOPATH...)
	@mkdir -p $(dir $@)
	@ln -sf $(CURDIR) $@

$(BINDIR): | $(BASE); $(info Creating build directory...)
	@cd $(BASE) && mkdir -p $@

buildbin: $(BINDIR)/$(BINNAME)
	$(info Done!)

$(BINDIR)/$(BINNAME): | $(BINDIR); $(info Building $(BINNAME)...)
	@cd $(BASE)/cmd/manager && $(GO_BUILD)

image:
	docker build -t openshift.io/ptp-operator -f Dockerfile.rhel7 .

clean:
	rm -rf build/_output/bin/ptp-operator
	rm -rf $(GOPATH)
