# drashta Makefile (03-architecture.md section 2, "Makefile targets").
# `install`, `service` and `unservice` change this Mac (PREFIX, launchd). Run them only on purpose.
# These targets are macOS-only. On Windows use the plain npm and go build commands from README.md.

BIN        := drashta
# Install dir: /opt/homebrew/bin when it exists (Apple Silicon Homebrew), else /usr/local/bin.
PREFIX     ?= $(shell if [ -d /opt/homebrew/bin ]; then echo /opt/homebrew/bin; else echo /usr/local/bin; fi)
LABEL      := in.drashta
PLIST      := $(LABEL).plist
AGENTS     := $(HOME)/Library/LaunchAgents
UID        := $(shell id -u)
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || date +%Y.%m.%d)
GOFLAGS_LD := -ldflags "-X main.version=$(VERSION)"

.PHONY: help web build go install dev test service unservice logs reindex clean

help:
	@echo "make build      web client + binary (SKIP_WEB=1 reuses web/build/client)"
	@echo "make go         only the Go binary ($(BIN))"
	@echo "make install    build, then copy the binary to $(PREFIX)  (changes this Mac)"
	@echo "make service    fill $(PLIST) and (re)start the launchd service  (changes this Mac)"
	@echo "make unservice  stop and remove the launchd service"
	@echo "make dev        Go server (-dev, 4455) plus Vite (5173)"
	@echo "make test       go vet, go test, web tests"
	@echo "make logs       tail the drashta and launchd logs"
	@echo "make reindex    drop cache tables, rescan, exit (service must be stopped)"
	@echo "make clean      remove the built binary"
	@echo "Variables: PREFIX=$(PREFIX) VERSION=$(VERSION) SKIP_WEB=1"

# Build the web client into web/build/client (embedded by the Go binary).
# NOTE: this runs `npm ci`, which deletes and reinstalls web/node_modules. Use SKIP_WEB=1 to avoid it.
web:
	npm --prefix web ci
	npm --prefix web run build

# Web client, then the binary. SKIP_WEB=1 reuses the web/build/client that is already there.
ifeq ($(SKIP_WEB),1)
build: go
else
build: web go
endif

# Only the Go binary (embeds whatever is in web/build/client).
go:
	go build -buildvcs=false $(GOFLAGS_LD) -o $(BIN) ./cmd/drashta

install: build
	install -m 0755 $(BIN) $(PREFIX)/$(BIN)

# Go server with -dev on 4455 plus Vite on 5173 (Vite proxies /api, SSE included).
dev:
	@trap 'kill 0' INT TERM EXIT; \
	go run ./cmd/drashta -dev -port 4455 & \
	npm --prefix web run dev; \
	wait

test:
	go vet ./...
	go test ./...
	npm --prefix web test

# Install and start the launchd service (same pattern as other local services). Changes this Mac.
service:
	mkdir -p $(HOME)/.drashta $(AGENTS)
	sed -e 's|@BINDIR@|$(PREFIX)|g' -e 's|@BIN@|$(PREFIX)/$(BIN)|g' -e 's|@HOME@|$(HOME)|g' $(PLIST) > $(AGENTS)/$(PLIST)
	-launchctl bootout gui/$(UID)/$(LABEL) 2>/dev/null
	@# bootstrap right after bootout can fail with "Input/output error" (5) while launchd is still
	@# unloading the old copy: wait for it to go, then retry a few times.
	@for i in 1 2 3 4 5 6 7 8; do \
	  launchctl print gui/$(UID)/$(LABEL) >/dev/null 2>&1 || break; sleep 1; \
	done
	@for i in 1 2 3 4 5; do \
	  launchctl bootstrap gui/$(UID) $(AGENTS)/$(PLIST) 2>/dev/null && exit 0; sleep 2; \
	done; \
	launchctl bootstrap gui/$(UID) $(AGENTS)/$(PLIST)

unservice:
	-launchctl bootout gui/$(UID)/$(LABEL)
	rm -f $(AGENTS)/$(PLIST)

logs:
	tail -F $(HOME)/.drashta/drashta.log $(HOME)/.drashta/launchd.log

# Drop the cache tables, rescan everything, exit. Refuses while the service runs.
reindex:
	go run ./cmd/drashta -reindex

clean:
	rm -f $(BIN)
