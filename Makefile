VERSION ?= v3.1.3
DIST_DIR ?= dist
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
BUILD_OUTPUT = jar2native$(if $(filter windows,$(GOOS)),.exe,)

.PHONY: build runners release test vet fmt clean e2e

runners:
	@mkdir -p runner/bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o runner/bin/runner-linux-amd64 ./runner/generic
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o runner/bin/runner-linux-arm64 ./runner/generic
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o runner/bin/runner-windows-amd64.exe ./runner/generic
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o runner/bin/runner-darwin-amd64 ./runner/generic
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o runner/bin/runner-darwin-arm64 ./runner/generic

build: runners
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BUILD_OUTPUT) .

release: runners test
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o $(DIST_DIR)/jar2native-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o $(DIST_DIR)/jar2native-linux-arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o $(DIST_DIR)/jar2native-darwin-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o $(DIST_DIR)/jar2native-darwin-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w' -o $(DIST_DIR)/jar2native-windows-amd64.exe .

test: vet
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

e2e:
	bash tests/e2e/run.sh

clean:
	rm -f dist/* jar2native jar2native.exe
	rm -f runner/bin/runner-*
	rm -rf $(DIST_DIR)
	rm -rf /tmp/jar2native-build-*
