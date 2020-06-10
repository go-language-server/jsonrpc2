# ----------------------------------------------------------------------------
# global

.DEFAULT_GOAL = test

# ----------------------------------------------------------------------------
# target

.PHONY: all
all: mod pkg/install

# ----------------------------------------------------------------------------
# include

include hack/make/go.mk

# ----------------------------------------------------------------------------
# overlays

.PHONY: test/gojay
test/gojay: GO_BUILDTAGS+=gojay
test/gojay: test

.PHONY: coverage/ci/gojay
coverage/ci/gojay: GO_BUILDTAGS+=gojay
coverage/ci/gojay: coverage/ci
	$(call target)

.PHONY: tools
tools:
	cd tools; \
	  for t in $$(go list -f '{{ join .Imports " " }}' -tags=tools); do \
	  	GOBIN=${CURDIR}/bin go install -v -x -mod=vendor "$${t}"; \
	  done
