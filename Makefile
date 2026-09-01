# HomeOS — top-level convenience targets.
#
# Each component has its own Makefile; this one exists so a contributor can run
# the whole thing without knowing which directory does what.

.PHONY: all build test install release dev demo clean docs

all: build

build:
	$(MAKE) -C backend build
	$(MAKE) -C web build

test:
	$(MAKE) -C backend test
	@echo
	@echo "Dashboard end-to-end checks need a running daemon:"
	@echo "  make -C web e2e"

# Installs onto a machine install.sh has already prepared.
install:
	$(MAKE) -C backend install
	$(MAKE) -C web install
	systemctl restart homeos-core

# Signed release archives plus a channel manifest. See docs/deployment.md.
release:
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
