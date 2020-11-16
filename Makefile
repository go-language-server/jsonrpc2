# ----------------------------------------------------------------------------
# global

.DEFAULT_GOAL := test
comma := ,
empty :=
space := $(empty) $(empty)

# ----------------------------------------------------------------------------
# go

GO_PATH ?= $(shell go env GOPATH)
GO_OS ?= $(shell go env GOOS)
GO_ARCH ?= $(shell go env GOARCH)
TOOLS_BIN=${CURDIR}/tools/bin

PKG := $(subst $(GO_PATH)/src/,,$(CURDIR))
GO_PKGS := $(shell go list ./... | grep -v -e '.pb.go')
GO_TEST_PKGS := $(shell go list -f='{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)

export GOTESTSUM_FORMAT=pkgname-and-test-fails
GO_TEST ?= ${TOOLS_BIN}/gotestsum --format=standard-verbose --
GO_TEST_FUNC ?= .
GO_TEST_FLAGS ?=
GO_BENCH_FUNC ?= .
GO_BENCH_FLAGS ?= -benchmem
GO_LINT_FLAGS ?=

CGO_ENABLED ?= 0
GO_BUILDTAGS=osusergo netgo static
GO_LDFLAGS=-s -w "-extldflags=-static"
GO_FLAGS ?= -tags='$(subst $(space),$(comma),${GO_BUILDTAGS})' -ldflags='${GO_LDFLAGS}' -installsuffix=netgo
GO_FLAGS_TOOLS ?= -tags='$(subst $(space),$(comma),${GO_BUILDTAGS})' -ldflags='-s -w' -installsuffix=netgo

# ----------------------------------------------------------------------------
# defines

GOPHER = ""
define target
@printf "$(GOPHER)  \\x1b[1;32m$(patsubst ,$@,$(1))\\x1b[0m\\n"
endef

# ----------------------------------------------------------------------------
# target

##@ tools

.PHONY: tools
tools: ${TOOLS_BIN}  ## Install tools

${TOOLS_BIN}:
	cd tools; \
	  for t in $$(go list -f '{{ join .Imports " " }}' -tags=tools); do \
	  	GOBIN=${TOOLS_BIN} CGO_ENABLED=0 go install -v -mod=mod ${GO_FLAGS_TOOLS} "$${t}"; \
	  done

##@ test, bench, coverage

.PHONY: test
test: ${TOOLS_BIN}
test: CGO_ENABLED=1
test: GO_FLAGS=-tags='$(subst $(space),$(comma),${GO_BUILDTAGS})'
test:  ## Runs package test including race condition.
	$(call target)
	CGO_ENABLED=$(CGO_ENABLED) $(GO_TEST) -race -count 1 -run=$(GO_TEST_FUNC) $(strip ${GO_FLAGS}) $(GO_TEST_PKGS)

.PHONY: test/gojay
test/gojay: GO_BUILDTAGS+=gojay
test/gojay: test

.PHONY: bench
bench:  ## Take a package benchmark.
	$(call target)
	CGO_ENABLED=$(CGO_ENABLED) $(GO_TEST) -run='^$$' -bench=$(GO_BENCH_FUNC) -benchmem $(strip $(GO_FLAGS)) $(GO_TEST_PKGS)

.PHONY: coverage
coverage: ${TOOLS_BIN}
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
lint/golangci-lint: ${TOOLS_BIN} .golangci.yml  ## Run golangci-lint.
	$(call target)
	@${TOOLS_BIN}/golangci-lint run $(strip ${GO_LINT_FLAGS}) ./...


##@ clean

.PHONY: clean
clean:  ## Cleanups binaries and extra files in the package.
	$(call target)
	@rm -rf *.out *.test *.prof trace.log ${TOOLS_BIN}


##@ miscellaneous

.PHONY: todo
TODO:  ## Print the all of (TODO|BUG|XXX|FIXME|NOTE) in packages.
	@grep -E '(TODO|BUG|XXX|FIXME|NOTE)(\(.+\):|:)' $(find . -type f -name '*.go' -and -not -iwholename '*vendor*')


##@ help

.PHONY: help
help:  ## Show this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[33m<target>\033[0m\n"} /^[a-zA-Z_0-9\/_-]+:.*?##/ { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
