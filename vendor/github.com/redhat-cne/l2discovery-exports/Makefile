GOLANGCI_VERSION=v1.60.3
GO_PATH=$(shell go env GOPATH)

.PHONY: all clean test build lint install-lint fmt

fmt:
	go fmt ./...

lint:
	golangci-lint run

# Install golangci-lint
install-lint:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GO_PATH)/bin $(GOLANGCI_VERSION)

test:
	go test -v ./...

build:
	go build ./...

clean:
	go clean

all: fmt build test lint
