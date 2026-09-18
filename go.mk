# include Makefile for Go related targets and variables
#
# Follows the conventions of the other includes in .make: every setting is an
# overridable variable documented for `make long-help`, every public target has
# pre/do/post hooks, and reports are written under
# $(GO_BUILD_REPORTS_DIRECTORY).
#
# Nothing in here is project specific. Projects set the variables below from
# their own Makefile, and extend behaviour by overriding the go-pre-<target>
# and go-post-<target> hooks - e.g. define go-pre-lint to compile a custom
# golangci-lint plugin, or go-pre-test to generate untracked fixtures.

.PHONY: metrics-collect-target
$(filter-out metrics-collect-target, $(MAKECMDGOALS)): metrics-collect-target

# do not declare targets if help had been invoked
ifneq (long-help,$(firstword $(MAKECMDGOALS)))
ifneq (help,$(firstword $(MAKECMDGOALS)))

SHELL=/usr/bin/env bash

GO_PROJECT_DIR ?= .## Go project root dir

GO_RUNNER ?= go## the go command to use

GO_BUILD_DIR ?= build## directory for build output and reports

GO_BUILD_REPORTS_DIRECTORY ?= $(GO_BUILD_DIR)/reports## build reports directory

GO_BINARY_NAME ?= $(if $(strip $(PROJECT_NAME)),$(PROJECT_NAME),$(shell basename $(CURDIR)))## name of the binary produced by go-build

GO_BINARY_DIR ?= bin## directory the binary is written to

GO_BUILD_TARGET ?= .## package to build

GO_BUILD_GOOS ?= ## GOOS for go-build - empty builds for the host

GO_BUILD_GOARCH ?= ## GOARCH for go-build - empty builds for the host

GO_CGO_ENABLED ?= 0## CGO_ENABLED for go-build - 1 is needed for cgo or an external linker

GO_BUILD_LDFLAGS ?= ## ldflags passed to go build, e.g. -X pkg/internal.Version=1.2.3

GO_BUILD_SWITCHES ?= ## custom switches passed to go build

GO_VARS_BEFORE_BUILD ?= ## environment variables to set before go build

GO_FMT_TARGET ?= ./...## packages to format

GO_VET_TARGET ?= ./...## packages to vet

GO_LINT_TARGET ?= ./...## packages to lint

GO_GOLANGCI_LINT_VERSION ?= latest## golangci-lint version to install - pin this per project

GO_SWITCHES_FOR_GOLANGCI_LINT ?= ## custom switches passed to golangci-lint

GO_TEST_TARGET ?= ./...## packages to test

GO_TEST_SWITCHES ?= ## custom switches passed to go test, e.g. -short or -race

GO_VARS_BEFORE_TEST ?= ## environment variables to set before go test

GO_TEST_REQUIRED_ENV ?= ## space separated environment variable names that must be set before testing

GO_GOTESTSUM_VERSION ?= latest## gotestsum version to install - pin this per project

GO_GOCOVER_COBERTURA_VERSION ?= latest## gocover-cobertura version to install - pin this per project

# -------------------------------------------------------------------------
# build
# -------------------------------------------------------------------------

go-pre-build:

go-post-build:

go-do-build:
	@cd $(GO_PROJECT_DIR); \
	mkdir -p $(GO_BINARY_DIR); \
	rm -f $(GO_BINARY_DIR)/$(GO_BINARY_NAME); \
	$(GO_VARS_BEFORE_BUILD) \
	CGO_ENABLED=$(GO_CGO_ENABLED) \
	$(if $(strip $(GO_BUILD_GOOS)),GOOS=$(GO_BUILD_GOOS),) \
	$(if $(strip $(GO_BUILD_GOARCH)),GOARCH=$(GO_BUILD_GOARCH),) \
	$(GO_RUNNER) build -o $(GO_BINARY_DIR)/$(GO_BINARY_NAME) \
		$(if $(strip $(GO_BUILD_LDFLAGS)),-ldflags '$(GO_BUILD_LDFLAGS)',) \
		$(GO_BUILD_SWITCHES) $(GO_BUILD_TARGET); \
	ls -l $(GO_BINARY_DIR)/$(GO_BINARY_NAME)

## TARGET: go-build
## SYNOPSIS: make go-build
## HOOKS: go-pre-build, go-post-build
## VARS:
##
##		GO_BINARY_NAME=<name of the binary to produce> - defaults to the project name
##		GO_BINARY_DIR=<directory to write the binary to> - default 'bin'
##		GO_BUILD_TARGET=<package to build> - default '.'
##		GO_BUILD_GOOS=<GOOS to build for> - empty builds for the host
##		GO_BUILD_GOARCH=<GOARCH to build for> - empty builds for the host
##		GO_CGO_ENABLED=<0|1> - default 0
##		GO_BUILD_LDFLAGS=<ldflags passed to go build>
##		GO_BUILD_SWITCHES=<switches passed to go build>
##		GO_VARS_BEFORE_BUILD=<environment variables to set before go build>
##
##  Build a Go binary into $(GO_BINARY_DIR)/$(GO_BINARY_NAME).

go-build: go-pre-build go-do-build go-post-build  ## build the go binary

.PHONY: go-build go-pre-build go-do-build go-post-build

# -------------------------------------------------------------------------
# format
# -------------------------------------------------------------------------

go-pre-format:

go-post-format:

go-do-format:
	@cd $(GO_PROJECT_DIR); $(GO_RUNNER) fmt $(GO_FMT_TARGET)

## TARGET: go-format
## SYNOPSIS: make go-format
## HOOKS: go-pre-format, go-post-format
## VARS:
##
##		GO_FMT_TARGET=<packages to format> - default './...'
##
##  Format the go code with go fmt.

go-format: go-pre-format go-do-format go-post-format  ## format the go code

.PHONY: go-format go-pre-format go-do-format go-post-format

# -------------------------------------------------------------------------
# vet
# -------------------------------------------------------------------------

go-pre-vet:

go-post-vet:

go-do-vet:
	@cd $(GO_PROJECT_DIR); \
	$(GO_RUNNER) mod download; \
	$(GO_RUNNER) vet $(GO_VET_TARGET)

## TARGET: go-vet
## SYNOPSIS: make go-vet
## HOOKS: go-pre-vet, go-post-vet
## VARS:
##
##		GO_VET_TARGET=<packages to vet> - default './...'
##
##  Report suspicious constructs with go vet.

go-vet: go-pre-vet go-do-vet go-post-vet  ## vet the go code

.PHONY: go-vet go-pre-vet go-do-vet go-post-vet

# -------------------------------------------------------------------------
# lint
# -------------------------------------------------------------------------

go-pre-lint:

go-post-lint:

# Installed from source so that any custom plugin built in go-pre-lint is
# compiled with the same toolchain and module versions as the linter itself.
# The version check is a prefix match (not equality) so it still short-circuits
# for a go-pre-lint-built custom-gcl binary, whose --short output is the pin
# plus a "-custom-gcl-<hash>" suffix, not the bare pin.
go-install-golangci-lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint version --short 2>/dev/null | grep -q "^$(GO_GOLANGCI_LINT_VERSION)" || \
		$(GO_RUNNER) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GO_GOLANGCI_LINT_VERSION)

go-do-lint: go-install-golangci-lint
	@cd $(GO_PROJECT_DIR); \
	mkdir -p $(GO_BUILD_REPORTS_DIRECTORY); \
	golangci-lint run \
		--output.junit-xml.path=$(GO_BUILD_REPORTS_DIRECTORY)/linting.xml \
		--output.text.path=stdout \
		$(GO_SWITCHES_FOR_GOLANGCI_LINT) $(GO_LINT_TARGET)

## TARGET: go-lint
## SYNOPSIS: make go-lint
## HOOKS: go-pre-lint, go-post-lint
## VARS:
##
##		GO_LINT_TARGET=<packages to lint> - default './...'
##		GO_GOLANGCI_LINT_VERSION=<golangci-lint version to install>
##		GO_SWITCHES_FOR_GOLANGCI_LINT=<switches passed to golangci-lint>
##		GO_BUILD_REPORTS_DIRECTORY=<directory to store build and test results>
##
##  Lint the go code with golangci-lint, writing a JUnit report to
##  $(GO_BUILD_REPORTS_DIRECTORY)/linting.xml

go-lint: go-pre-lint go-do-lint go-post-lint  ## lint the go code

.PHONY: go-lint go-pre-lint go-do-lint go-post-lint go-install-golangci-lint

# -------------------------------------------------------------------------
# test
# -------------------------------------------------------------------------

go-test-preflight:
	@for v in $(GO_TEST_REQUIRED_ENV); do \
		if [ -z "$${!v}" ]; then \
			echo "$$v is not set but is required to run the tests"; \
			exit 1; \
		fi; \
	done

go-pre-test:

go-post-test:

go-install-test-tools:
	@command -v gotestsum >/dev/null 2>&1 || \
		$(GO_RUNNER) install gotest.tools/gotestsum@$(GO_GOTESTSUM_VERSION)
	@command -v gocover-cobertura >/dev/null 2>&1 || \
		$(GO_RUNNER) install github.com/boumenot/gocover-cobertura@$(GO_GOCOVER_COBERTURA_VERSION)

go-do-test: go-install-test-tools
	@cd $(GO_PROJECT_DIR); \
	mkdir -p $(GO_BUILD_REPORTS_DIRECTORY); \
	$(GO_VARS_BEFORE_TEST) gotestsum \
		--junitfile $(GO_BUILD_REPORTS_DIRECTORY)/unit-tests.xml \
		--format testname \
		-- -coverprofile=$(GO_BUILD_REPORTS_DIRECTORY)/coverage.out -covermode=atomic \
		$(GO_TEST_SWITCHES) $(GO_TEST_TARGET); \
	gocover-cobertura < $(GO_BUILD_REPORTS_DIRECTORY)/coverage.out \
		> $(GO_BUILD_REPORTS_DIRECTORY)/code-coverage.xml

## TARGET: go-test
## SYNOPSIS: make go-test
## HOOKS: go-pre-test, go-post-test
## VARS:
##
##		GO_TEST_TARGET=<packages to test> - default './...'
##		GO_TEST_SWITCHES=<switches passed to go test, e.g. -short or -race>
##		GO_TEST_REQUIRED_ENV=<env var names that must be set before testing>
##		GO_VARS_BEFORE_TEST=<environment variables to set before go test>
##		GO_BUILD_REPORTS_DIRECTORY=<directory to store build and test results>
##
##  Test the go code, writing a JUnit report to
##  $(GO_BUILD_REPORTS_DIRECTORY)/unit-tests.xml and a cobertura coverage
##  report to $(GO_BUILD_REPORTS_DIRECTORY)/code-coverage.xml

go-test: go-pre-test go-test-preflight go-do-test go-post-test  ## test the go code

.PHONY: go-test go-pre-test go-test-preflight go-do-test go-post-test go-install-test-tools

endif
endif
