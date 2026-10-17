# Shortcuts for the Wails tasks. wails3 lives in $(go env GOPATH)/bin, which
# isn't always on PATH: call it by path, and export PATH for the tasks that run it themselves.
GOBIN := $(shell go env GOPATH)/bin
export PATH := $(PATH):$(GOBIN)
WAILS := $(GOBIN)/wails3

.PHONY: dev build install server bindings db test test-it test-frontend e2e

dev:            ## run with hot reload
	$(WAILS) task dev

build:          ## desktop app -> bin/Chloe
	$(WAILS) task build

install:        ## signed Chloe.app -> /Applications (quits a running copy)
	$(WAILS) task install

server:         ## browser build -> bin/Chloe-server (used by e2e)
	$(WAILS) task build:server

bindings:       ## regenerate frontend bindings after changing a service
	$(WAILS) task common:generate:bindings

db:             ## start and seed the Docker databases (CHLOE_DB_SLOT=N for a separate set)
	testdata/db.sh

test:           ## Go unit tests (SQLite included, no Docker)
	$(WAILS) task test

test-it:        ## start and seed the Docker databases, run integration tests
	$(WAILS) task test:it

test-frontend:  ## type check, React hooks lint and frontend unit tests
	cd frontend && npx tsc --noEmit && npx eslint src && npx vitest run

e2e: server     ## Playwright against the browser build: make e2e [SCRIPT=smoke]
	e2e/run.sh $(or $(SCRIPT),smoke)
