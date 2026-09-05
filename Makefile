.PHONY: build test vet fmt clean e2e

build:
	CGO_ENABLED=0 go build -o jar2native .

test: vet
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

e2e:
	bash tests/e2e/run.sh

clean:
	rm -f jar2native
	rm -rf /tmp/jar2native-build-*
