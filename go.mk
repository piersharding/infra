# include Makefile for Go related targets and variables
#
# This file follows the ska-cicd-makefile conventions (pre/do/post hooks,
# self-documenting variables, build/reports output) so that it can be lifted
# into that repository unchanged.

# do not declare targets if help had been invoked
ifneq (long-help,$(firstword $(MAKECMDGOALS)))
ifneq (help,$(firstword $(MAKECMDGOALS)))

SHELL=/usr/bin/env bash

GO_PROJECT_DIR ?= .## Go project root dir

GO_BUILD_DIR ?= build## build output directory

GO_BUILD_REPORTS_DIRECTORY ?= $(GO_BUILD_DIR)/reports## build reports directory

GO_BINARY_NAME ?= infra## name of the binary produced by go-build

GO_BINARY_DIR ?= bin## directory the binary is written to

GO_MODULE ?= github.com/infrahq/infra## go module path, used to build ldflags

GO_BUILD_VERSION ?= 0.0.0## version stamped into the binary

GO_TELEMETRY_WRITE_KEY ?= none## telemetry write key stamped into the binary

GO_CGO_ENABLED ?= 1## CGO_ENABLED value - the static build and the lint plugin both need cgo

GO_BUILD_LDFLAGS_EXTRA ?= -linkmode external -extldflags "-static"## extra ldflags appended to the defaults

GO_BUILD_LDFLAGS ?= -s -X $(GO_MODULE)/internal.Version="v$(GO_BUILD_VERSION)" -X $(GO_MODULE)/internal.TelemetryWriteKey="$(GO_TELEMETRY_WRITE_KEY)" $(GO_BUILD_LDFLAGS_EXTRA)## ldflags passed to go build

GO_BUILD_SWITCHES ?= ## custom switches passed to go build

GO_BUILD_TARGET ?= .## package to build

GO_LINT_TARGET ?= ./...## packages to lint

# Must stay on the v1 module path: .golangci.yml uses the v1 configuration
# schema (linters-settings, issues.exclude-dirs). golangci-lint v2 lives at
# a /v2 module path, so `@latest` here resolves to the newest v1 release.
GO_GOLANGCI_LINT_VERSION ?= v1.64.8## pinned golangci-lint version - do not use @latest

GO_SWITCHES_FOR_GOLANGCI_LINT ?= ## custom switches passed to golangci-lint

GO_QUERYLINTER_DIR ?= internal/tools/querylinter/cmd## directory holding the custom golangci-lint plugin

GO_QUERYLINTER_PLUGIN ?= querylinter.so## custom golangci-lint plugin, built before linting

GO_TEST_TARGET ?= ./...## packages to test

GO_TEST_SWITCHES ?= ## custom switches passed to go test

GO_GOTESTSUM_VERSION ?= v1.8.2## pinned gotestsum version

GO_GOCOVER_COBERTURA_VERSION ?= latest## pinned gocover-cobertura version

GO_TEST_REQUIRES_POSTGRES ?= true## when true, go-test fails early unless POSTGRESQL_CONNECTION is set

GO_TEST_PRE_COMMAND ?= ## optional shell command run before the suite, for fixtures the tests need but the repo does not track

GO_VET_TARGET ?= ./...## packages to vet

GO_FMT_TARGET ?= ./...## packages to format

# -------------------------------------------------------------------------
# build
# -------------------------------------------------------------------------

go-pre-build:

go-post-build:

go-do-build:
	@cd $(GO_PROJECT_DIR); \
	mkdir -p $(GO_BINARY_DIR); \
	rm -f $(GO_BINARY_DIR)/$(GO_BINARY_NAME); \
	CGO_ENABLED=$(GO_CGO_ENABLED) GOOS=linux go build \
		-o $(GO_BINARY_DIR)/$(GO_BINARY_NAME) \
		-ldflags '$(GO_BUILD_LDFLAGS)' \
		$(GO_BUILD_SWITCHES) $(GO_BUILD_TARGET); \
	ls -l $(GO_BINARY_DIR)/$(GO_BINARY_NAME)

## TARGET: go-build
## SYNOPSIS: make go-build
## HOOKS: go-pre-build, go-post-build
## VARS:
##
##		GO_BINARY_NAME=<name of the binary to produce> - default 'infra'
##		GO_BINARY_DIR=<directory to write the binary to> - default 'bin'
##		GO_BUILD_TARGET=<package to build> - default '.'
##		GO_BUILD_VERSION=<version stamped into the binary>
##		GO_BUILD_LDFLAGS=<ldflags passed to go build>
##		GO_BUILD_SWITCHES=<switches passed to go build>
##
##  Build a statically linked Go binary.

go-build: go-pre-build go-do-build go-post-build  ## build the go binary

.PHONY: go-build go-pre-build go-do-build go-post-build

# -------------------------------------------------------------------------
# format
# -------------------------------------------------------------------------

go-pre-format:

go-post-format:

go-do-format:
	@cd $(GO_PROJECT_DIR); go fmt $(GO_FMT_TARGET)

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
	go mod download; \
	go vet $(GO_VET_TARGET)

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

# golangci-lint is installed from source so that the custom plugin below is
# built with the exact same Go toolchain and module versions as the linter
# itself. A mismatch makes the plugin fail to load at runtime.
go-install-golangci-lint:
	@command -v golangci-lint >/dev/null 2>&1 || \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GO_GOLANGCI_LINT_VERSION)

go-do-lint: go-install-golangci-lint
	@cd $(GO_PROJECT_DIR); \
	mkdir -p $(GO_BUILD_REPORTS_DIRECTORY); \
	(cd $(GO_QUERYLINTER_DIR); go build -o $(GO_QUERYLINTER_PLUGIN) -buildmode=plugin .); \
	golangci-lint run \
		--out-format junit-xml:$(GO_BUILD_REPORTS_DIRECTORY)/linting.xml,colored-line-number \
		$(GO_SWITCHES_FOR_GOLANGCI_LINT) $(GO_LINT_TARGET)

## TARGET: go-lint
## SYNOPSIS: make go-lint
## HOOKS: go-pre-lint, go-post-lint
## VARS:
##
##		GO_LINT_TARGET=<packages to lint> - default './...'
##		GO_GOLANGCI_LINT_VERSION=<pinned golangci-lint version>
##		GO_SWITCHES_FOR_GOLANGCI_LINT=<switches passed to golangci-lint>
##		GO_QUERYLINTER_DIR=<directory holding the custom plugin>
##		GO_BUILD_REPORTS_DIRECTORY=<directory to store build and test results>
##
##  Lint the go code with golangci-lint, building any custom plugin first.
##  Writes a JUnit report to $(GO_BUILD_REPORTS_DIRECTORY)/linting.xml

go-lint: go-pre-lint go-do-lint go-post-lint  ## lint the go code

.PHONY: go-lint go-pre-lint go-do-lint go-post-lint go-install-golangci-lint

# -------------------------------------------------------------------------
# test
# -------------------------------------------------------------------------

go-pre-test:
ifeq ($(GO_TEST_REQUIRES_POSTGRES),true)
	@if [ -z "$$POSTGRESQL_CONNECTION" ]; then \
		echo "POSTGRESQL_CONNECTION is not defined. Use \`make postgres\` if you need to start postgres,"; \
		echo "or set GO_TEST_REQUIRES_POSTGRES=false to run without it."; \
		exit 1; \
	fi
endif

go-post-test:

go-install-test-tools:
	@command -v gotestsum >/dev/null 2>&1 || \
		go install gotest.tools/gotestsum@$(GO_GOTESTSUM_VERSION)
	@command -v gocover-cobertura >/dev/null 2>&1 || \
		go install github.com/boumenot/gocover-cobertura@$(GO_GOCOVER_COBERTURA_VERSION)

go-do-test: go-install-test-tools
	@cd $(GO_PROJECT_DIR); \
	mkdir -p $(GO_BUILD_REPORTS_DIRECTORY); \
	if [ -n "$(strip $(GO_TEST_PRE_COMMAND))" ]; then $(GO_TEST_PRE_COMMAND); fi; \
	gotestsum \
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
##		GO_TEST_REQUIRES_POSTGRES=<true|false> - guard on POSTGRESQL_CONNECTION
##		GO_TEST_PRE_COMMAND=<command run before the suite, for untracked fixtures>
##		GO_BUILD_REPORTS_DIRECTORY=<directory to store build and test results>
##
##  Test the go code. Writes a JUnit report to
##  $(GO_BUILD_REPORTS_DIRECTORY)/unit-tests.xml and a cobertura coverage
##  report to $(GO_BUILD_REPORTS_DIRECTORY)/code-coverage.xml

go-test: go-pre-test go-do-test go-post-test  ## test the go code

.PHONY: go-test go-pre-test go-do-test go-post-test go-install-test-tools

endif
endif
