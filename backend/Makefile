# HomeOS backend.
#
# The output is a single static binary, which is what makes the phase-4 OTA
# update a file replacement rather than a package transaction.

BINARY      := homeos-core
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PREFIX      ?= /usr/lib/homeos
GO          ?= go

# CGO off keeps the binary static: it then runs on any glibc or musl system
# without matching the build host's libc.
GOFLAGS     := CGO_ENABLED=0
LDFLAGS     := -s -w -X main.Version=$(VERSION)

.PHONY: all build build-all release-tool test vet fmt lint clean install run check

all: vet test build

build:
	$(GOFLAGS) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/homeos-core

# Both appliance targets, named for the release artefacts.
build-all:
	$(GOFLAGS) GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
		-o dist/$(BINARY)-linux-amd64 ./cmd/homeos-core
	$(GOFLAGS) GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
		-o dist/$(BINARY)-linux-arm64 ./cmd/homeos-core

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...
	gofmt -l -s .

clean:
	rm -rf $(BINARY) homeos-release dist .testbin

# $(PREFIX)/bin is a symlink into the current release directory, so this lands
# inside the release install.sh made current — which is what keeps a source
# install and an over-the-air update using the same layout.
install: build
	install -d -m 0755 $(DESTDIR)$(PREFIX)/bin
	install -m 0755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	@echo "installed to $(DESTDIR)$(PREFIX)/bin/$(BINARY)"
	@echo "restart with: systemctl restart homeos-core"

# The release-channel tool. A build-host utility, never installed on an
# appliance: it holds the signing workflow, not the runtime.
release-tool:
	$(GOFLAGS) $(GO) build -trimpath -o homeos-release ./cmd/homeos-release

run: build
	./$(BINARY) -config ./dev/config.yaml

check: build
	./$(BINARY) -config /etc/homeos/config.yaml -check
