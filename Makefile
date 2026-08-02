BINARY  := vmstat
MODULE  := github.com/trentas/vmstat-macos
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Installs into ~/.local/bin by default, which needs no sudo. For a system-wide
# install use: sudo make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build ./bin/$(BINARY)
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .
	@echo "built bin/$(BINARY) ($(VERSION))"

.PHONY: install
install: build ## Install $(BINARY) into $(BINDIR)
	@mkdir -p "$(BINDIR)"
	install -m 0755 bin/$(BINARY) "$(BINDIR)/$(BINARY)"
	@echo "installed $(BINDIR)/$(BINARY)"
	@command -v $(BINARY) >/dev/null 2>&1 || \
		echo "warning: $(BINDIR) is not on your PATH; add it to run '$(BINARY)' directly"

.PHONY: uninstall
uninstall: ## Remove $(BINARY) from $(BINDIR)
	rm -f "$(BINDIR)/$(BINARY)"
	@echo "removed $(BINDIR)/$(BINARY)"

.PHONY: test
test: ## Run the test suite
	go test ./...

.PHONY: race
race: ## Run the test suite under the race detector
	go test -race ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the source
	gofmt -w .

.PHONY: fmtcheck
fmtcheck: ## Fail if any file is not gofmt-clean
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi
	@echo "gofmt ok"

.PHONY: check
check: fmtcheck vet test ## Run every check

.PHONY: run
run: build ## Build and run with a 1s interval
	./bin/$(BINARY) 1

.PHONY: snapshot
snapshot: ## Build release artifacts locally without publishing
	HOMEBREW_TAP_TOKEN=unused goreleaser release --snapshot --clean --skip=publish

.PHONY: release
release: ## Publish the current tag (needs goreleaser and a gh login)
	@git describe --exact-match --tags >/dev/null 2>&1 \
		|| { echo "HEAD is not tagged; run: git tag -a vX.Y.Z -m ... && git push origin vX.Y.Z"; exit 1; }
	GITHUB_TOKEN="$$(gh auth token)" HOMEBREW_TAP_TOKEN="$$(gh auth token)" \
		goreleaser release --clean

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist

.PHONY: help
help: ## List the available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
