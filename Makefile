all: bin

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
