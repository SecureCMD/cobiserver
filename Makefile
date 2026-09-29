SHELL := /bin/sh

# Dependencies are vendored (see vendor/, committed) so every build here
# is network-free and reproducible; -mod=vendor makes that mandatory
# rather than an implicit default, so a missing/stale vendor/ fails loudly
# instead of silently falling back to the network. Re-run `go mod vendor`
# after any go.mod change.
export GOFLAGS := -mod=vendor

# ---- metadata baked into the binary (see `koserver version`) ----
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

MODULE      := cobiserver
BIN_NAME    := koserver
CMD_PATH    := ./cmd/koserver
BIN_DIR     := bin
DIST_DIR    := dist

# Native build target: overridable, e.g. `make build GOOS=linux GOARCH=arm64`
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Platforms shipped by `make dist` / `make release`. armv7 covers 32-bit
# Raspberry Pi-class boards, a common self-hosting target for this kind of
# service.
DIST_PLATFORMS := linux/amd64 linux/arm64 linux/arm/7 darwin/arm64

IMAGE      ?= cobiserver/koserver
DOCKER_TAG ?= $(VERSION)

# systemd install locations (used by `make install`/`uninstall`, Linux only)
PREFIX          ?= /usr/local
SYSTEMD_UNIT_DIR ?= /etc/systemd/system
CONFIG_DIR       ?= /etc/koserver
DATA_DIR         ?= /var/lib/koserver
SERVICE_USER     ?= koserver
SUDO := $(shell [ "$$(id -u)" != "0" ] && command -v sudo || true)

.PHONY: help build run test vet fmt fmt-check lint clean \
	dist release checksums deb \
	docker-build docker-buildx docker-run \
	install uninstall systemd-reload

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: ## Build for GOOS/GOARCH (defaults to host; override e.g. GOOS=linux GOARCH=arm64)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/$(BIN_NAME)$(if $(filter windows,$(GOOS)),.exe,) $(CMD_PATH)
	@echo "built $(BIN_DIR)/$(BIN_NAME)$(if $(filter windows,$(GOOS)),.exe,) ($(GOOS)/$(GOARCH), $(VERSION))"

run: ## Run the server locally with `go run` (no build artifact)
	go run $(CMD_PATH) serve

test: ## Run the test suite
	go test ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Reformat all Go source with gofmt
	gofmt -w .

fmt-check: ## Fail if any file is not gofmt-formatted
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed on:"; echo "$$unformatted"; exit 1; \
	fi

lint: fmt-check vet ## fmt-check + vet

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) $(DIST_DIR)

# ---- cross-compiled release builds ----
# CGO is off project-wide (pure-Go SQLite driver), so cross-compiling for
# any GOOS/GOARCH pair needs no C toolchain.

dist: clean ## Cross-compile release binaries for DIST_PLATFORMS into dist/
	@mkdir -p $(DIST_DIR)
	@for p in $(DIST_PLATFORMS); do \
		os=$$(echo $$p | cut -d/ -f1); \
		arch=$$(echo $$p | cut -d/ -f2); \
		arm=$$(echo $$p | cut -d/ -f3); \
		out=$(DIST_DIR)/$(BIN_NAME)_$(VERSION)_$${os}_$${arch}$${arm:+v$${arm}}; \
		mkdir -p $$out; \
		echo "building $$os/$$arch$${arm:+ (armv$$arm)}..."; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=$$arm \
			go build -trimpath -ldflags "$(LDFLAGS)" -o $$out/$(BIN_NAME)$(if $(filter windows,$$os),.exe,) $(CMD_PATH) || exit 1; \
		cp README.md LICENSE $$out/ 2>/dev/null || true; \
		tar -C $(DIST_DIR) -czf $$out.tar.gz $$(basename $$out); \
		rm -rf $$out; \
	done
	@$(MAKE) --no-print-directory checksums

checksums: ## Regenerate dist/SHA256SUMS for everything currently in dist/
	@cd $(DIST_DIR) && shasum -a 256 *.tar.gz > SHA256SUMS 2>/dev/null || sha256sum *.tar.gz > SHA256SUMS
	@echo "wrote $(DIST_DIR)/SHA256SUMS"

release: dist ## Alias for `dist`

# ---- Debian package ----

deb: ## Build a .deb (needs: dpkg-dev debhelper golang-go git); output lands one directory up
	dpkg-buildpackage -us -uc -b

# ---- Docker ----

docker-build: ## Build the single-arch Docker image for the host's architecture
	docker build -t $(IMAGE):$(DOCKER_TAG) --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

docker-buildx: ## Build (and optionally push, PUSH=1) a multi-arch image: linux/amd64,linux/arm64
	docker buildx build --platform linux/amd64,linux/arm64 \
		-t $(IMAGE):$(DOCKER_TAG) \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
		$(if $(PUSH),--push,--load) .

docker-run: docker-build ## Build and run the image locally on :8080
	docker run --rm -it -p 8080:8080 -e ALLOW_SIGNUP=true $(IMAGE):$(DOCKER_TAG)

# ---- systemd install (Linux hosts only; run on the target server, not here) ----

install: ## Install the binary + systemd unit on a Linux host (needs root; builds for host arch)
	@if [ "$$(uname -s)" != "Linux" ]; then \
		echo "make install targets Linux/systemd; run it on the server, not $$(uname -s)"; exit 1; \
	fi
	$(MAKE) --no-print-directory build GOOS=linux GOARCH=$$(go env GOARCH)
	$(SUDO) install -Dm755 $(BIN_DIR)/$(BIN_NAME) $(PREFIX)/bin/$(BIN_NAME)
	$(SUDO) getent passwd $(SERVICE_USER) >/dev/null || \
		$(SUDO) useradd --system --no-create-home --shell /usr/sbin/nologin $(SERVICE_USER)
	$(SUDO) install -d -o $(SERVICE_USER) -g $(SERVICE_USER) -m 0750 $(DATA_DIR)
	$(SUDO) install -d -m 0750 $(CONFIG_DIR)
	$(SUDO) test -f $(CONFIG_DIR)/koserver.env || \
		$(SUDO) install -m 0640 packaging/systemd/koserver.env.example $(CONFIG_DIR)/koserver.env
	$(SUDO) install -m 0644 packaging/systemd/koserver.service $(SYSTEMD_UNIT_DIR)/koserver.service
	$(SUDO) systemctl daemon-reload
	@echo
	@echo "Installed. Next steps:"
	@echo "  1. Edit $(CONFIG_DIR)/koserver.env (BASE_URL, ALLOW_SIGNUP, SMTP_*, ...)"
	@echo "  2. sudo systemctl enable --now koserver"
	@echo "  3. journalctl -u koserver -f"

uninstall: ## Stop and remove the systemd service and binary (keeps DATA_DIR by default)
	@if [ "$$(uname -s)" != "Linux" ]; then \
		echo "make uninstall targets Linux/systemd; run it on the server, not $$(uname -s)"; exit 1; \
	fi
	-$(SUDO) systemctl disable --now koserver
	$(SUDO) rm -f $(SYSTEMD_UNIT_DIR)/koserver.service
	$(SUDO) systemctl daemon-reload
	$(SUDO) rm -f $(PREFIX)/bin/$(BIN_NAME)
	@echo "Removed service and binary. Left in place: $(DATA_DIR) (your data) and $(CONFIG_DIR) (your config)."
	@echo "Delete them manually if you really want a clean slate."

systemd-reload: ## Reload the unit file and restart the running service
	$(SUDO) systemctl daemon-reload
	$(SUDO) systemctl restart koserver
