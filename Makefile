# HomeOS — top-level convenience targets.
#
# Each component has its own Makefile; this one exists so a contributor can run
# the whole thing without knowing which directory does what.

.PHONY: all build test install release dev demo clean docs toolchain

all: build

# A missing compiler shows up as a bare "127" from make, which tells you
# nothing. Name what is missing and how to get it.
GO_REQUIRED := $(shell awk '/^go [0-9]/ {print $2; exit}' backend/go.mod)

toolchain:
	@missing=""; 	command -v go  >/dev/null 2>&1 || missing="$missing go"; 	command -v npm >/dev/null 2>&1 || missing="$missing npm"; 	if [ -n "$missing" ]; then 	  echo ""; 	  echo "Missing build tools:$missing"; 	  echo ""; 	  echo "  HomeOS needs Go $(GO_REQUIRED)+ and Node 18+ to build from source."; 	  echo "  No distribution packages a new enough Go, so install.sh does not"; 	  echo "  install it: an appliance has no business carrying a compiler."; 	  echo ""; 	  echo "  Install them with:"; 	  echo "      sudo scripts/install-build-deps.sh"; 	  echo ""; 	  echo "  Or build on another machine and copy the result across —"; 	  echo "  see docs/deployment.md."; 	  echo ""; 	  exit 1; 	fi

build: toolchain
	$(MAKE) -C backend build
	$(MAKE) -C web build

test: toolchain
	$(MAKE) -C backend test
	@echo
	@echo "Dashboard end-to-end checks need a running daemon:"
	@echo "  make -C web e2e"

# Installs onto a machine install.sh has already prepared.
install: toolchain
	$(MAKE) -C backend install
	$(MAKE) -C web install
	systemctl restart homeos-core

# Signed release archives plus a channel manifest. See docs/deployment.md.
release: toolchain
	@test -n "$(VERSION)"  || (echo "usage: make release VERSION=1.1.0 BASE_URL=https://..." && false)
	@test -n "$(BASE_URL)" || (echo "usage: make release VERSION=1.1.0 BASE_URL=https://..." && false)
	scripts/build-release.sh "$(VERSION)" "$(BASE_URL)"

dev:
	docker compose -f docker-compose.dev.yml up --build

demo:
	docker compose -f docker-compose.demo.yml up --build

clean:
	$(MAKE) -C backend clean
	$(MAKE) -C web clean
	rm -rf dist
