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

install-go: build-go
	install -m 0755 bridge-go $(HOME)/.local/bin/bridge-go
