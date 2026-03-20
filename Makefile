BINARY   := jar2native
VERSION  := 2.0.0
LDFLAGS  := -ldflags "-s -w -X main.version=$(VERSION)"
GOFLAGS  := CGO_ENABLED=0

# ── Default target ──────────────────────────────────────────────────────────
.PHONY: all
all: build

# ── Build jar2native tool for current platform ───────────────────────────────
.PHONY: build
build:
	$(GOFLAGS) go build $(LDFLAGS) -o $(BINARY) .
	@echo "Built: ./$(BINARY)"

# ── Cross-platform release builds of jar2native itself ───────────────────────
.PHONY: release
release: release-darwin-arm64 release-darwin-amd64 release-linux-amd64 release-linux-arm64 release-windows-amd64

.PHONY: release-darwin-arm64
release-darwin-arm64:
	GOOS=darwin  GOARCH=arm64 $(GOFLAGS) go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 .

.PHONY: release-darwin-amd64
release-darwin-amd64:
	GOOS=darwin  GOARCH=amd64 $(GOFLAGS) go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 .

.PHONY: release-linux-amd64
release-linux-amd64:
	GOOS=linux   GOARCH=amd64 $(GOFLAGS) go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 .

.PHONY: release-linux-arm64
release-linux-arm64:
	GOOS=linux   GOARCH=arm64 $(GOFLAGS) go build $(LDFLAGS) -o dist/$(BINARY)-linux-arm64 .

.PHONY: release-windows-amd64
release-windows-amd64:
	GOOS=windows GOARCH=amd64 $(GOFLAGS) go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe .

# ── Install jar2native to $GOPATH/bin ────────────────────────────────────────
.PHONY: install
install:
	$(GOFLAGS) go install $(LDFLAGS) .
	@echo "Installed: $$(go env GOPATH)/bin/$(BINARY)"

# ── Run tests ────────────────────────────────────────────────────────────────
.PHONY: test
test:
	go test ./...

# ── Clean ────────────────────────────────────────────────────────────────────
.PHONY: clean
clean:
	rm -f $(BINARY) $(BINARY).exe
	rm -rf dist/

# ── Help ─────────────────────────────────────────────────────────────────────
.PHONY: help
help:
	@echo "Targets for building jar2native itself:"
	@echo "  build                  Build for current platform  → ./jar2native"
	@echo "  release                Build for all platforms     → dist/"
	@echo "  install                Install to GOPATH/bin"
	@echo "  test                   Run tests"
	@echo "  clean                  Remove build artifacts"
	@echo ""
	@echo "Using jar2native to package a Java app:"
	@echo "  ./jar2native app.jar                    # produces dist/app"
	@echo "  ./jar2native app.jar --os linux         # cross-compile for Linux"
	@echo "  ./jar2native app.jar --all-modules      # include all JDK modules"
