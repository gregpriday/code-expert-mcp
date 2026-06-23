# CodeExpert — build & install
#
# Common targets:
#   make            Build the ./codeexpert binary (CGO-free, version-stamped)
#   make install    Install codeexpert globally (default: /usr/local/bin)
#   make uninstall  Remove the installed binary
#   make test       Unit + end-to-end + no-write invariant
#   make help       List all targets
#
# Install location is overridable:
#   make install PREFIX=$HOME/.local      -> $HOME/.local/bin/codeexpert
#   make install BINDIR=/opt/homebrew/bin -> /opt/homebrew/bin/codeexpert
# If $(BINDIR) is not writable, run: sudo make install
# DESTDIR is honored for staged/packaged installs (e.g. make DESTDIR=/tmp/pkg install).

# Strict shell for reliable recipes.
SHELL       := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

GO      ?= go
BINARY  := codeexpert
PKG     := ./cmd/codeexpert

PREFIX  ?= /usr/local
BINDIR  ?= $(PREFIX)/bin
DESTDIR ?=

# Module path is discovered dynamically so version stamping survives a repo rename.
MODULE  := $(shell $(GO) list -m)
# Version stamped into internal/app.Version; override with: make VERSION=1.2.3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X $(MODULE)/internal/app.Version=$(VERSION)

# The default build is CGO-free by design; keep it that way.
export CGO_ENABLED = 0

.PHONY: all build install uninstall test test-race vet fmt fmt-check clean help

all: build

build: ## Build ./codeexpert (CGO-free, version-stamped)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) $(PKG)

install: build ## Install globally (override PREFIX/BINDIR/DESTDIR)
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 $(BINARY) "$(DESTDIR)$(BINDIR)/$(BINARY)"
	@echo "Installed $(BINARY) $(VERSION) -> $(DESTDIR)$(BINDIR)/$(BINARY)"

uninstall: ## Remove the installed binary
	rm -f "$(DESTDIR)$(BINDIR)/$(BINARY)"
	@echo "Removed $(DESTDIR)$(BINDIR)/$(BINARY)"

test: ## Unit + end-to-end + no-write invariant
	$(GO) test ./...

test-race: ## Race detector on the concurrent workflow passes + evidence store (needs cgo)
	CGO_ENABLED=1 $(GO) test -race ./internal/workflow/ ./internal/evidence/

vet: ## go vet ./...
	$(GO) vet ./...

fmt: ## Format internal/ and cmd/ in place
	gofmt -w internal/ cmd/

fmt-check: ## Fail if any file under internal/ or cmd/ needs gofmt
	@out="$$(gofmt -l internal/ cmd/)"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

clean: ## Remove the local build artifact
	rm -f $(BINARY)

help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
