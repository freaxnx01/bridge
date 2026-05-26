.PHONY: help test smoke lint test-deps all build-go test-go

BATS ?= bats

help:
	@echo "Targets:"
	@echo "  make test       Run bats unit tests under tests/unit"
	@echo "  make smoke      Run tests/smoke.sh (shellcheck + --version/--help)"
	@echo "  make lint       Run shellcheck on all shell scripts (all severities)"
	@echo "  make all        Run smoke + test"
	@echo "  make test-deps  Show how to install bats and shellcheck"

all: smoke test

test:
	$(BATS) tests/unit

smoke:
	tests/smoke.sh

lint:
	shellcheck -s bash -x \
	  bridge.sh \
	  bridge-autosync.sh \
	  bridge-unpushed-warn.sh \
	  bridge-watcher.sh \
	  setup-claude-channels.sh

test-deps:
	@echo "Install bats and shellcheck:"
	@echo "  Debian/Ubuntu: sudo apt install bats shellcheck"
	@echo "  macOS:         brew install bats-core shellcheck"
	@echo "  From source:   git clone https://github.com/bats-core/bats-core /opt/bats"
	@echo "                 and add /opt/bats/bin to PATH"

build-go:
	go build \
		-ldflags "-X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev) -X main.commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o bridge-go ./cmd/bridge

test-go:
	go test ./...

# install-go installs the Go binary as `bridge` on PATH. The bash bridge()
# function (still sourced from ~/.bashrc until Plan C task 3) shadows this
# in interactive shells, so this is safe to run before cutover. After cutover,
# the shim's `command bridge` resolves here.
install-go: build-go
	install -m 0755 bridge-go $(HOME)/.local/bin/bridge
	@# Clean up the previous installed name to avoid two binaries on PATH.
	@rm -f $(HOME)/.local/bin/bridge-go

.PHONY: install-shim
install-shim:
	install -d $(HOME)/.local/share/bridge
	install -m 0644 shims/bridge-shim.sh $(HOME)/.local/share/bridge/bridge-shim.sh
	@echo
	@echo "Shim installed to $(HOME)/.local/share/bridge/bridge-shim.sh"
	@echo "DO NOT add to ~/.bashrc yet — Phase 3 (Plan C) handles cutover."
