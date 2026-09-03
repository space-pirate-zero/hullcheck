# Every target here is a gate. hullcheck reads this file and holds the project to it.
GO ?= go
BIN := hullcheck
VERSION ?= dev
MODULE := github.com/spaceship-alpha-9/hullcheck

.PHONY: all check fmt vet test deps network readonly secrets dogfood refusals build clean

all: check

## check: the full gate suite, in the order that fails cheapest first
check: fmt vet deps network secrets test readonly dogfood refusals

## fmt: every file gofmt-clean (CONTRIBUTING 1.3)
fmt:
	@out=$$(gofmt -l . ); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi
	@echo "fmt   ok"

## vet: no go vet findings (CONTRIBUTING 1.5, 2.2)
vet:
	@$(GO) vet ./...
	@echo "vet   ok"

## test: all tests, race detector on (CONTRIBUTING 1.4, 3.1, 3.2)
test:
	@$(GO) test -race -count=1 ./...
	@echo "test  ok"

## deps: zero third-party dependencies (CONTRIBUTING 1.1)
## Two independent checks: go.mod declares nothing, and nothing outside the
## standard library or this module appears in the resolved dependency graph.
## A dot in the first path element is what marks a package as external - our own
## module path contains dots too, so it must be excluded explicitly.
deps:
	@if grep -qE '^\s*require' go.mod; then \
		echo "go.mod declares dependencies:"; grep -nE '^\s*require' go.mod; exit 1; fi
	@ext=$$($(GO) list -deps ./... \
		| grep -v '^$(MODULE)' \
		| awk -F/ '$$1 ~ /\./ {print}' || true); \
	if [ -n "$$ext" ]; then echo "third-party dependencies found:"; echo "$$ext"; exit 1; fi
	@echo "deps  ok (zero third-party)"

## network: the core must not import a network package (CONTRIBUTING 1.2)
network:
	@if $(GO) list -deps ./cmd/hullcheck ./internal/... | grep -E '^(net|net/http)$$'; then \
		echo "core imports a network package"; exit 1; fi
	@echo "net   ok (no network package in the core)"

## secrets: nothing credential-shaped in the tree (CONTRIBUTING 3.3)
## Uses grep, not `git grep`. hullcheck --verify caught the git version as FAKE:
## git grep only searches TRACKED files, and where there is no git repository it
## errors, which the shell reads as "no match" - so the gate passed no matter what.
## A gate that cannot fail is worse than no gate.
secrets:
	@if grep -rEIl --exclude-dir=.git --exclude-dir=vendor --exclude-dir=node_modules \
		--exclude=Makefile --exclude=.hullcheck.yml \
		'(BEGIN [A-Z ]*PRIVATE KEY|AKIA[0-9A-Z]{16}|sk-[A-Za-z0-9]{20,})' . ; then \
		echo "possible secret in the tree"; exit 1; fi
	@echo "sec   ok"

## readonly: hullcheck must not write to the repo it reads (CONTRIBUTING 1.6)
readonly:
	@$(GO) build -o /tmp/$(BIN)-ro ./cmd/hullcheck
	@before=$$(git status --porcelain | sort); \
	/tmp/$(BIN)-ro --no-banner . >/dev/null 2>&1 || true; \
	after=$$(git status --porcelain | sort); \
	if [ "$$before" != "$$after" ]; then echo "hullcheck modified the working tree"; exit 1; fi
	@echo "ro    ok (working tree unchanged)"

## refusals: prove hullcheck stops when it says it stops (CONTRIBUTING 3.6)
refusals: build
	@./$(BIN) --no-banner --refusals .
	@echo "ref   ok"

## dogfood: hullcheck scores itself (CONTRIBUTING 1.7, 2.1)
dogfood: build
	@./$(BIN) --no-banner --fail-under 80 .
	@echo "dog   ok"

build:
	@$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/hullcheck

clean:
	@rm -f $(BIN) hc-probe
