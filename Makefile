# Requires Go 1.23+ (see go.mod). With Go 1.22, set GOTOOLCHAIN=auto to fetch the toolchain.
export GOTOOLCHAIN ?= auto

.PHONY: build test race vet lint e2e clean testprograms

build: vet testprograms
	go build -o taskmasterd ./cmd/daemon
	go build -o taskmasterctl ./cmd/ctl

testprograms:
	go build -o testprograms/crasher/crasher ./testprograms/crasher/
	go build -o testprograms/envreporter/envreporter ./testprograms/envreporter/
	go build -o testprograms/longrunner/longrunner ./testprograms/longrunner/
	go build -o testprograms/slowstopper/slowstopper ./testprograms/slowstopper/
	go build -o testprograms/ticker/ticker ./testprograms/ticker/

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

e2e:
	go test -v ./e2e -count=1 -timeout 120s

clean:
	rm -f taskmasterd taskmasterctl


