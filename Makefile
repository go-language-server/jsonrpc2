# ----------------------------------------------------------------------------
# global

SHELL=/usr/bin/env bash
.DEFAULT_GOAL=test
comma := ,
empty :=
space := $(empty) $(empty)

# ----------------------------------------------------------------------------
# go

ifneq ($(shell command -v go),)
GO_PATH ?= $(shell go env GOPATH)
GO_OS ?= $(shell go env GOOS)
GO_ARCH ?= $(shell go env GOARCH)
TOOLS_BIN=${CURDIR}/bin

PKG := $(subst $(GO_PATH)/src/,,$(CURDIR))
GO_PKGS := $(shell go list ./... | grep -v -e '.pb.go')
GO_TEST_PKGS := $(shell go list -f='{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)

export GOTESTSUM_FORMAT=pkgname-and-test-fails
GO_TEST ?= go test
GO_TEST_FUNC ?= .
GO_TEST_FLAGS ?=
GO_BENCH_FUNC ?= .
GO_BENCH_FLAGS ?= -benchmem
GO_LINT_FLAGS ?=

CGO_ENABLED ?= 0
GO_BUILDTAGS=osusergo netgo static static_build
GO_LDFLAGS=-s -w "-extldflags=-static"
GO_FLAGS ?= -tags='$(subst $(space),$(comma),${GO_BUILDTAGS})' -ldflags='${GO_LDFLAGS}' -installsuffix=netgo
endif

# ----------------------------------------------------------------------------
# defines

GOPHER = ""
define target
@printf "$(GOPHER)  \\x1b[1;32m$(patsubst ,$@,$(1))\\x1b[0m\\n"
endef

# ----------------------------------------------------------------------------
# target

.PHONY: all
all: mod pkg/install

.PHONY: pkg/install
pkg/install:
	$(call target)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GO_OS) GOARCH=$(GO_ARCH) go install -v $(shell go list -f '{{if and (or .GoFiles .CgoFiles) (ne .Name "main")}}{{.ImportPath}}{{end}}' ${PKG}/...)

##@ tools

.PHONY: tools
tools: ## Install tools
	cd tools; \
		go mod vendor -v; \
	  for t in $$(go list -f '{{ join .Imports " " }}' -tags=tools); do \
	  	GOBIN=${CURDIR}/bin go install -v -x -mod=vendor "$${t}" > /dev/null 2>&1; \
	  done

##@ test, bench and coverage

.PHONY: test
test: CGO_ENABLED=1
test:  ## Runs package test including race condition.
	$(call target)
	CGO_ENABLED=$(CGO_ENABLED) $(GO_TEST) -v -race -count 1 -run=$(GO_TEST_FUNC) $(strip ${GO_FLAGS}) $(GO_TEST_PKGS)

.PHONY: test/gojay
test/gojay: GO_BUILDTAGS+=gojay
test/gojay: test

.PHONY: bench
bench:  ## Take a package benchmark.
	$(call target)
	CGO_ENABLED=$(CGO_ENABLED) $(GO_TEST) -run='^$$' -bench=$(GO_BENCH_FUNC) -benchmem $(strip $(GO_FLAGS)) $(GO_TEST_PKGS)

.PHONY: coverage
coverage: GO_TEST=${TOOLS_BIN}/gotestsum --
coverage: CGO_ENABLED=1
coverage:  ## Takes packages test coverage.
	$(call target)
	CGO_ENABLED=$(CGO_ENABLED) $(GO_TEST) -race -covermode=atomic -coverpkg=$(PKG)/... -coverprofile=coverage.out $(strip $(GO_FLAGS)) $(GO_PKGS)

coverage/gojay: GO_BUILDTAGS+=gojay
coverage/gojay: coverage

##@ lint

.PHONY: lint
lint: lint/golangci-lint  ## Run all linters.

.PHONY: lint/golangci-lint
lint/golangci-lint: tools .golangci.yml  ## Run golangci-lint.
	$(call target)
	@${TOOLS_BIN}/golangci-lint run $(strip ${GO_LINT_FLAGS}) ./...


##@ clean

.PHONY: clean
clean:  ## Cleanups binaries and extra files in the package.
	$(call target)
	@$(RM) $(APP) *.out *.test *.prof trace.log


##@ miscellaneous

.PHONY: AUTHORS
AUTHORS:  ## Creates AUTHORS file.
	@$(file >$@,# This file lists all individuals having contributed content to the repository.)
	@$(file >>$@,# For how it is generated, see `make AUTHORS`.)
	@printf "$(shell git log --format="\n%aN <%aE>" | LC_ALL=C.UTF-8 sort -uf)" >> $@

.PHONY: todo
TODO:  ## Print the all of (TODO|BUG|XXX|FIXME|NOTE) in packages.
	@rg -e '(TODO|BUG|XXX|FIXME|NOTE)(\(.+\):|:)' --follow --hidden --glob='!.git' --glob='!vendor' --glob='!internal' --glob='!Makefile' --glob='!snippets' --glob='!indent'


##@ help

.PHONY: help
help:  ## Show make target help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[33m<target>\033[0m\n"} /^[a-zA-Z_0-9\/_-]+:.*?##/ { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
