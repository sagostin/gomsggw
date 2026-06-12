# GOMSGGW Makefile
#
# Targets:
#   make test        — run the gateway's unit tests
#   make test-verbose — run the gateway's unit tests with verbose output
#   make build       — build the Docker image (delegates to ./build.sh)
#   make lint        — run go vet
#   make tidy        — go mod tidy
#
# The default `test` target runs the root package only. The `migration/`
# subdirectory has two colliding `package main` files (a pre-existing repo
# bug) and `scripts/` contains an unrelated main, so we exclude both.

GO ?= go

# Packages we test. Root package only by default — add the legacy
# vendored SMPP library if you want its tests too.
TEST_PKGS := .

.PHONY: test
test:
	$(GO) test -count=1 -race $(TEST_PKGS)

.PHONY: test-verbose
test-verbose:
	$(GO) test -count=1 -race -v $(TEST_PKGS)

.PHONY: build
build:
	./build.sh

.PHONY: lint
lint:
	$(GO) vet $(TEST_PKGS)

.PHONY: tidy
tidy:
	$(GO) mod tidy
