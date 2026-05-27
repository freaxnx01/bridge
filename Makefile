.PHONY: help all build-go test-go test-shim install install-go install-shim

BATS ?= bats

help:
	@echo "Targets:"
	@echo "  make install      Install binary + shell shim (recommended default)"
	@echo "  make build-go     Build the Go binary into ./bridge-go"
	@echo "  make test-go      Run go test ./..."
	@echo "  make test-shim    Run shims/bridge-shim.bats (requires bats)"
	@echo "  make install-go   Install Go binary as ~/.local/bin/bridge (no shim)"
	@echo "  make install-shim Install bridge-shim.sh to ~/.local/share/bridge/"
	@echo "  make all          Run test-go + test-shim"

all: test-go test-shim

test-go:
	go test ./...

test-shim:
	$(BATS) shims/bridge-shim.bats

build-go:
	go build \
		-ldflags "-X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o bridge-go ./cmd/bridge

# install-go installs the Go binary as `bridge` on PATH.
install-go: build-go
	install -m 0755 bridge-go $(HOME)/.local/bin/bridge
	@# Clean up the previous installed name to avoid two binaries on PATH.
	@rm -f $(HOME)/.local/bin/bridge-go

install-shim:
	install -d $(HOME)/.local/share/bridge
	install -m 0644 shims/bridge-shim.sh $(HOME)/.local/share/bridge/bridge-shim.sh
	install -m 0644 shims/bridge-completion-meta.sh $(HOME)/.local/share/bridge/bridge-completion-meta.sh
	@echo
	@echo "Shim installed to $(HOME)/.local/share/bridge/bridge-shim.sh"
	@echo "Completion meta-augmenter at $(HOME)/.local/share/bridge/bridge-completion-meta.sh (optional)"
	@echo "See go-migrate.md for the ~/.bashrc source line."

# install bundles the binary + shell shim. Verbs like `open` and `sessions
# attach` need the shim to actually cd/attach, so binary-only installs leave
# the user with a partially-broken bridge. Make this the recommended default.
install: install-go install-shim
	@echo
	@echo 'Bridge installed. Start a new shell (or `source ~/.bashrc`) to pick up the shim.'
