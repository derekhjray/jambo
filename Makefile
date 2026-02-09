JAMBO_VERSION=1.0.0

.PHONY: all build test clean install

all: build

build:
	@echo "Building jambo..."
	go build -o jambo ./cmd/jambo

test:
	@echo "Running tests..."
	go test ./...

clean:
	@echo "Cleaning..."
	rm -f jambo
	rm -rf dist

install:
	@echo "Installing..."
	go install ./cmd/jambo

release: clean test build
	@echo "Creating release..."
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -o dist/jambo-linux-amd64 ./cmd/jambo
	GOOS=darwin GOARCH=amd64 go build -o dist/jambo-darwin-amd64 ./cmd/jambo
	GOOS=darwin GOARCH=arm64 go build -o dist/jambo-darwin-arm64 ./cmd/jambo

example:
	@echo "Building example..."
	cd examples/basic && go build -o basic

help:
	@echo "Available targets:"
	@echo "  build     - Build the jambo binary"
	@echo "  test      - Run tests"
	@echo "  clean     - Clean build artifacts"
	@echo "  install   - Install to GOPATH/bin"
	@echo "  release   - Build release binaries for multiple platforms"
	@echo "  example   - Build the example program"
	@echo "  help      - Show this help message"