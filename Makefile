.PHONY: build test vet fmt clean e2e release all

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

build:
	@if command -v go >/dev/null 2>&1; then \
		mkdir -p dist; \
		CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="-s -w" -o dist/jar2native-$(GOOS)-$(GOARCH) .; \
	else \
		echo "go not found. Please install Go or run in CI."; exit 1; \
	fi

test: vet
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

e2e:
	bash tests/e2e/run.sh

release:
	mkdir -p dist
	@for p in linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64; do \
		OS=$${p%/*}; ARCH=$${p#*/}; \
		echo "Building $$OS/$$ARCH..."; \
		OUT=dist/jar2native-$$OS-$$ARCH; \
		if [ "$$OS" = "windows" ]; then OUT=$$OUT.exe; fi; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -ldflags="-s -w" -o $$OUT . || exit 1; \
	done

clean:
	rm -f dist/* jar2native
	rm -rf /tmp/jar2native-build-*
