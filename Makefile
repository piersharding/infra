
DOCKER_CONTEXT ?= .
REPOSITORY_USER ?= ska-telescope
REPOSITORY_NAME ?= external/infra
DOCKER_REGISTRY ?= registry.gitlab.com/$(REPOSITORY_USER)/$(REPOSITORY_NAME)
TAG ?= 0.21.4
GITLAB_TOKEN ?=

# define overides for above variables in here
-include PrivateRules.mak


test: check-psql-env
	go test -short ./...

test-all: check-psql-env
	go test ./...

# update the expected command output file
test/update:
	go test ./internal/cmd -test.update-golden


fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	pwd
	env | grep GO
	ls -latr *
	go mod download
	go vet ./...

# add the git tag based on $TAG, and push.  This is the tag that goreleaser will use
git-tag-and-push:
	git tag v$(TAG)
	git push --tags

# must install goreleaser first - https://goreleaser.com/install/
release-artefacts-local: ## build the release artefacts locally to test what will happen
	rm -rf dist
	RELEASE_NAME=v$(TAG) goreleaser release --snapshot --clean

release-artefacts: ## build the release artefacts and publish ti gitlab
	rm -rf dist
	RELEASE_NAME=v$(TAG) GITLAB_TOKEN=$(GITLAB_TOKEN) goreleaser release --verbose --clean --skip announce,validate

docker/%:
	docker buildx build $(DOCKER_CONTEXT) --load -t infrahq/$*:dev

docker-build: fmt vet
	docker buildx build $(DOCKER_CONTEXT) --load -t $(DOCKER_REGISTRY)/infra:$(TAG)
	docker buildx build $(DOCKER_CONTEXT)/ui --load -t $(DOCKER_REGISTRY)/ui:$(TAG)

# perform update of go dependencies
go-update:
	go get -u ./...

# perform update of node dependencies
npm-update:
	cd ui && npm update
	cd ui && npm audit fix
	cd ui && npm audit fix --force

docker-push:
	docker push $(DOCKER_REGISTRY)/infra:$(TAG)
	docker push $(DOCKER_REGISTRY)/ui:$(TAG)

docker/ui: DOCKER_CONTEXT=ui

dev: dev/server

dev/context:
	kubectl config use-context docker-desktop

helm/update:
	helm repo update infrahq

dev/server: dev/context docker/infra docker/ui helm/update
	helm upgrade --install --wait \
		--set-string server.image.pullPolicy=Never \
		--set-string server.image.tag=dev \
		--set-string server.podAnnotations.checksum=$$(docker images -q infrahq/infra:dev) \
		--set-string ui.image.pullPolicy=Never \
		--set-string ui.image.tag=dev \
		--set-string ui.podAnnotations.checksum=$$(docker images -q infrahq/ui:dev) \
		infra-server infrahq/infra-server \
		$(flags)

dev/connector: dev/context docker/infra
	helm upgrade --install --wait \
		--set-string connector.image.pullPolicy=Never \
		--set-string connector.image.tag=dev \
		--set-string connector.podAnnotations.checksum=$$(docker images -q infrahq/infra:dev) \
		infra infrahq/infra \
		$(flags)

dev/clean:
	kubectl config use-context docker-desktop
	helm uninstall infra-server || true
	helm uninstall infra || true

postgres:
	docker run -d --name=postgres-dev --rm \
		-e POSTGRES_PASSWORD=password123 \
		--tmpfs=/var/lib/postgresql/data \
		-p 127.0.0.1:15432:5432 \
		postgres:14-alpine -c fsync=off -c full_page_writes=off \
			-c max_connections=100
	@echo
	@echo Copy the line below into the shell used to run tests
	@echo 'export POSTGRESQL_CONNECTION="host=localhost port=15432 user=postgres dbname=postgres password=password123"'


LINT_ARGS ?= --fix

# install from source, because we need to build our plugin with the exact
# same version of Go, and the exact same version of all Go modules.
golangci-lint:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.49.0

lint: golangci-lint internal/tools/querylinter/cmd/querylinter.so
	golangci-lint run $(LINT_ARGS)

%.so:
	(cd $(@D); go build -o $(@F) -buildmode=plugin .)

.PHONY: docs/api/openapi3.json
docs/api/openapi3.json:
	go run -ldflags '-s -X github.com/infrahq/infra/internal.Version=0.0.0' ./internal/openapigen $@

check-psql-env:
ifndef POSTGRESQL_CONNECTION
	$(error POSTGRESQL_CONNECTION is not defined. Use `make postgres` if you need to start postgres)
endif
