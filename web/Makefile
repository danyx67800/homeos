# HomeOS dashboard.
#
# The build output is static files. Phase 1's Caddyfile serves them from
# /opt/homeos/web with an index.html fallback, and homeos-core serves the same
# directory as a fallback when Caddy is not in front.

PREFIX  ?= /opt/homeos
WEBROOT := $(PREFIX)/web

.PHONY: all install build dev e2e clean

all: build

build:
	# Rollup's native binary is a per-platform optional dependency, so
	# --omit=optional breaks the build on every platform.
	npm ci
	npm run build

dev:
	npm run dev

# Requires a running homeos-core on 127.0.0.1:8791 serving this build.
e2e:
	npm run e2e

# rsync --delete so a removed asset does not linger; the hashed filenames mean
# a stale file would otherwise never be overwritten.
install: build
	install -d -m 0755 $(DESTDIR)$(WEBROOT)
	rsync -a --delete dist/ $(DESTDIR)$(WEBROOT)/
	@echo "installed to $(DESTDIR)$(WEBROOT)"

clean:
	rm -rf dist node_modules shots
