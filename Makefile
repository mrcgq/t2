MODULE  := github.com/user/tls-client-gui
BINARY  := tls-client-gui
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build run clean cross test

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY) ./cmd/tls-client-gui/
	@echo "Built: dist/$(BINARY)"

run: build
	./dist/$(BINARY) --log-level=debug

run-with-config: build
	./dist/$(BINARY) --config=../tls-client/configs/example.yaml --log-level=debug

clean:
	rm -rf dist/

test:
	go test -v -race ./...

cross:
	@echo ">>> linux/amd64"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/tls-client-gui/
	@echo ">>> linux/arm64"
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/tls-client-gui/
	@echo ">>> darwin/amd64"
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 ./cmd/tls-client-gui/
	@echo ">>> darwin/arm64"
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 ./cmd/tls-client-gui/
	@echo ">>> windows/amd64"
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe ./cmd/tls-client-gui/
	@echo ">>> windows/arm64"
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BINARY)-windows-arm64.exe ./cmd/tls-client-gui/
	@echo "All builds complete"
	ls -la dist/
