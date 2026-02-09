# Get version from git tag, fallback to git describe, or use "dev" if no tags
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || git describe --tags --always 2>/dev/null || echo "dev")

# Build flags to inject version
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

# Release build flags (strip debug info and symbol table)
RELEASE_LDFLAGS := -ldflags "-X main.version=$(VERSION) -w -s"

.PHONY: all build test clean install version

all: build

version:
	@echo "Version: $(VERSION)"

build:
	@echo "Building jambo $(VERSION)..."
	go build $(LDFLAGS) -o jambo ./cmd/jambo

test:
	@echo "Running tests..."
	go test ./...

clean:
	@echo "Cleaning..."
	rm -f jambo
	rm -rf dist

install:
	@echo "Installing jambo $(VERSION)..."
	go install $(LDFLAGS) ./cmd/jambo

release: clean test build
	@echo "Creating release $(VERSION)..."
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build $(RELEASE_LDFLAGS) -o dist/jambo-linux-amd64 ./cmd/jambo
	GOOS=darwin GOARCH=amd64 go build $(RELEASE_LDFLAGS) -o dist/jambo-darwin-amd64 ./cmd/jambo
	GOOS=darwin GOARCH=arm64 go build $(RELEASE_LDFLAGS) -o dist/jambo-darwin-arm64 ./cmd/jambo
	@echo "Release binaries created in dist/"
	@echo "Binary sizes:"
	@ls -lh dist/ | tail -n +2

example:
	@echo "Building example..."
	cd examples/basic && go build -o basic

help:
	@echo "Available targets:"
	@echo "  version   - Show current version from git tag"
	@echo "  build     - Build the jambo binary"
	@echo "  test      - Run tests"
	@echo "  clean     - Clean build artifacts"
	@echo "  install   - Install to GOPATH/bin"
	@echo "  release   - Build release binaries for multiple platforms"
	@echo "  example   - Build the example program"
	@echo "  help      - Show this help message"