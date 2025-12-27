SHELL=/usr/bin/env bash
ifneq ($(OS_NAME),darwin)
IP=$(shell (ip a 2> /dev/null || ifconfig) | sed -En 's/127.0.0.1//;s/.*inet (addr:)?(([0-9]*\.){3}[0-9]*).*/\2/p' | head -n1)
HOST=$(shell hostname -f)
else
IP=$(shell scutil --nwi | grep 'address' | cut -d':' -f2 | tr -d ' ' | head -n1)
HOST=$(shell hostname -s)
endif
SERVER_DEV=$(IP)
INFRA_ACCESS_KEY ?= 06e294c1bb.f636105ae3142c1c896fe1f9
INFRA_PASSWORD ?= Passw0rd1!Thing

DOCKER_ENGINE ?= docker
DOCKER_CONTEXT ?= .
REPOSITORY_USER ?= ska-telescope
REPOSITORY_NAME ?= external/infra
DOCKER_HOST ?= registry.gitlab.com
DOCKER_REGISTRY ?= $(DOCKER_HOST)/$(REPOSITORY_USER)/$(REPOSITORY_NAME)
TAG ?= 0.21.7
GITLAB_TOKEN ?=

LINT_ARGS ?= --fix

# define overides for above variables in here
-include PrivateRules.mak


clean: helm-clean clean-infra clean-infra-ui clean-postgres clean-secrets

docker-login:
	docker login $(DOCKER_HOST) -u$(REPOSITORY_USER) -p $(GITLAB_TOKEN)

test: check-psql-env
	go test -short ./...

test-all: check-psql-env test-npm
	go test ./...

test-npm: ## run npm tests
	cd ui && npm test

# update the expected command output file
test/update:
	go test ./internal/cmd -test.update-golden

fmt: ## Run go fmt against code.
	go fmt ./...

vet: ## Run go vet against code.
	pwd
	env | grep GO || true
	go mod download
	go vet ./...

GO_BUILD_LDFLAGS ?= -s -X github.com/infrahq/infra/internal.Version="v$(TAG)" \
					-X github.com/infrahq/infra/internal.TelemetryWriteKey="none" \
					-linkmode external -extldflags "-static"
build: ## build infra
	mkdir -p bin && rm -rf bin/infra
	CGO_ENABLED=1 GOOS=linux go build -o bin/infra -ldflags '$(GO_BUILD_LDFLAGS)' .
	ls -latr bin/
	./bin/infra --help

.PHONY: bin/infra
bin/infra: build ## build local bin/infra

version: ## current image version
	@echo "$(TAG)"

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
	$(DOCKER_ENGINE) buildx build $(DOCKER_CONTEXT) --load -t $(DOCKER_REGISTRY)/infra/$*:$(TAG)

docker-build: fmt vet
	$(DOCKER_ENGINE) buildx build $(DOCKER_CONTEXT) --build-arg BUILDVERSION=v$(TAG) --load -t $(DOCKER_REGISTRY)/infra:$(TAG)
	$(DOCKER_ENGINE) buildx build $(DOCKER_CONTEXT)/ui --load -t $(DOCKER_REGISTRY)/ui:$(TAG)

# perform update of go dependencies
go-update:
	go get -u ./...

# perform update of node dependencies
npm-update:
	cd ui && npm update
	cd ui && npm audit fix
	cd ui && npm audit fix --force

docker-push:
	$(DOCKER_ENGINE) push $(DOCKER_REGISTRY)/infra:$(TAG)
	$(DOCKER_ENGINE) push $(DOCKER_REGISTRY)/ui:$(TAG)

load:
	minikube image load $(DOCKER_REGISTRY)/infra:$(TAG)
	minikube image load $(DOCKER_REGISTRY)/ui:$(TAG)

docker/ui: DOCKER_CONTEXT=ui


.PHONY: dev-infra-server-vars
dev-infra-server-vars:
	$(eval INFRA_URL=$(shell kubectl -n infra get service infra-server -o jsonpath="{.status.loadBalancer.ingress[*]['ip', 'hostname']}"):80)
	$(eval INFRA_ACCESS_KEY=$(shell kubectl -n infra get secret infra-server-access-key -o jsonpath='{.data.access-key}' | base64 -d))

.PHONY: dev-test-data
dev-test-data: dev-infra-server-vars ## Create test data in dev Minikube environment
	make test-data INFRA_URL=$(INFRA_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY)

.PHONY: dev
dev: ## Deploy dev environment in Minikube
	make dev/server TAG=dev
	make dev-infra-server-vars
	make dev-test-data

.PHONY: un-dev
un-dev: ## Clean the dev environment in Minikube
	make dev/clean TAG=dev

dev/context:
	kubectl config use-context minikube

dev/server: dev/context docker/infra docker/ui load
	kubectl create ns infra || true
	kubectl -n infra create secret generic infra-server-access-key --from-literal=access-key=$(INFRA_ACCESS_KEY) || true
	kubectl -n infra create secret generic infra-server-initial-admin-secret --from-literal=password=$(INFRA_PASSWORD) || true
	kubectl -n infra label secret infra-server-initial-admin-secret "app.kubernetes.io/managed-by=Helm" "meta.helm.sh/release-name=infra-server"|| true
	kubectl -n infra annotate secret infra-server-initial-admin-secret "meta.helm.sh/release-namespace=infra" "meta.helm.sh/release-name=infra-server"|| true

	helm upgrade -n infra --install --wait \
    	--set-string config.admin.enable=true \
    	--set-string config.admin.accessKeySecret=infra-server-access-key \
	    --set-string server.service.type=LoadBalancer \
		--set-string server.image.pullPolicy=Never \
		--set-string server.image.repository=$(DOCKER_REGISTRY)/infra \
		--set-string server.image.tag=$(TAG) \
		--set-string server.podAnnotations.checksum=$$($(DOCKER_ENGINE) images -q $(DOCKER_REGISTRY)/infra/infra:$(TAG)) \
	    --set-string ui.service.type=LoadBalancer \
		--set-string ui.image.pullPolicy=Never \
		--set-string ui.image.repository=$(DOCKER_REGISTRY)/ui \
		--set-string ui.image.tag=$(TAG) \
		--set-string ui.podAnnotations.checksum=$$($(DOCKER_ENGINE) images -q $(DOCKER_REGISTRY)/infra/ui:$(TAG)) \
		infra-server ./charts/infra-server \
		$(flags)

dev/connector: dev/context docker/infra
	helm upgrade -n infra --install --wait \
		--set-string connector.image.pullPolicy=Never \
		--set-string connector.image.repository=$(DOCKER_REGISTRY)/infra \
		--set-string connector.image.tag=dev \
		--set-string connector.podAnnotations.checksum=$$($(DOCKER_ENGINE) images -q $(DOCKER_REGISTRY)/infra:$(TAG)) \
		infra ./charts/infra \
		$(flags)

.PHONY: dev/clean
dev/clean: dev/context
	helm -n infra uninstall infra-server || true
	helm -n infra uninstall infra || true
	kubectl delete ns infra || true

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

.PHONY: check-psql-env
check-psql-env:
ifndef POSTGRESQL_CONNECTION
	$(error POSTGRESQL_CONNECTION is not defined. Use `make postgres` if you need to start postgres)
endif

.PHONY: dev-oci
dev-oci: clean-oci ## launch dev container environment withi initial test data
	make gen-secrets
	make postgres
	sleep 3
	make infra-ui
	make infra-server
	make test-data
	@echo "Password: $$(cat $(CONF_DIR)/initial-admin-password-secret/password)"

.PHONY: postgres
postgres: ## deploy posgres container
	$(DOCKER_ENGINE) run -d --name=postgres-dev --rm \
		-e POSTGRES_DB=infra \
		-e POSTGRES_USER=infra \
		-e POSTGRES_PASSWORD=infra \
		-e PGDATA=/var/lib/postgresql/data/pgdata \
		--tmpfs=/var/lib/postgresql/data \
		-p 5432:5432 \
		postgres:14-alpine -c fsync=off -c full_page_writes=off \
			-c max_connections=100
	@echo
	@echo Copy the line below into the shell used to run tests
	@echo 'export POSTGRESQL_CONNECTION="host=localhost port=5432 user=infra dbname=infra password=infra"'

CONF_DIR ?= internal/server/testdata
SECRETS_DIR ?= $(CONF_DIR)/pki

.PHONY: gen-secrets
gen-secrets: ## create all secrets and certs
	mkdir -p \
	$(CONF_DIR) \
	$(SECRETS_DIR) \
	$(CONF_DIR)/encryption-key \
	$(CONF_DIR)/initial-admin-password-secret \
	$(CONF_DIR)/initial-admin-access-key-secret

	# must be 32 char rand string and no newline
	[ -f  $(CONF_DIR)/encryption-key/key ] || openssl rand -hex 16 | tr -d '\n' > $(CONF_DIR)/encryption-key/key
	# must be 16 char rand string and no newline
	[ -f  $(CONF_DIR)/initial-admin-password-secret/password ] || openssl rand -hex 8 | tr -d '\n' > $(CONF_DIR)/initial-admin-password-secret/password

	# secret seal key 10.24
	[ -f $(CONF_DIR)/initial-admin-access-key-secret/access-key ] || echo -n $$(openssl rand -hex 5).$$(openssl rand -hex 12) > \
		$(CONF_DIR)/initial-admin-access-key-secret/access-key

	[ -f $(SECRETS_DIR)/ca.key ] || openssl ecparam -name prime256v1 \
		-genkey -noout -out \
		$(SECRETS_DIR)/ca.key
	[ -f $(SECRETS_DIR)/ca-public.pem ] || openssl ec -in \
		$(SECRETS_DIR)/ca.key \
		-pubout -out  $(SECRETS_DIR)/ca-public.pem
	[ -f $(SECRETS_DIR)/ca.crt ] || openssl req -x509 -new -key \
		$(SECRETS_DIR)/ca.key \
		-days 3650 -out \
		$(SECRETS_DIR)/ca.crt \
		-subj "/CN=Infra Server"

.PHONY: clean-oci
clean-oci: clean-infra clean-infra-ui clean-postgres

.PHONY: clean-infra
clean-infra: ## clean infra server container
	$(DOCKER_ENGINE) rm -f infra || true

.PHONY: clean-infra-ui
clean-infra-ui: ## clean infra ui container
	$(DOCKER_ENGINE) rm -f infra-ui || true

.PHONY: clean-secrets
clean-secrets: # clean secrets
	rm -rf $(SECRETS_DIR) \
    	$(CONF_DIR)/encryption-key \
    	$(CONF_DIR)/initial-admin-password-secret \
    	$(CONF_DIR)/initial-admin-access-key-secret

.PHONY: clean-postgres
clean-postgres: ## clean postgres container
	$(DOCKER_ENGINE) rm -f postgres-dev || true

.PHONY: infra-server
infra-server: ## deploy infra server container
	$(DOCKER_ENGINE) rm -f infra || true
	$(DOCKER_ENGINE) run -d --name=infra \
		-e INFRA_SERVER_DB_PASSWORD=infra \
		-v $$(pwd)/internal:/home/infra/internal \
		--network host \
		-p 8080:8080 \
		-p 8443:8443 \
		-p 9090:9090 \
		$(DOCKER_REGISTRY)/infra:$(TAG) \
		server \
		-f \
		/home/infra/internal/server/testdata/infra.yaml \
		--log-level \
		debug

.PHONY: infra-ui
infra-ui: ## deploy ui container
	$(DOCKER_ENGINE) rm -f infra-ui || true
	$(DOCKER_ENGINE) run -d --name=infra-ui \
		-e INFRA_SERVER_DB_PASSWORD=infra \
		-e NODE_DEBUG=http \
		-e LOG_LEVEL=debug \
		-p 3000:3000 \
		$(DOCKER_REGISTRY)/ui:$(TAG)

.PHONY: get-access-key
get-access-key:
	$(eval INFRA_ACCESS_KEY:=$(shell cat $(CONF_DIR)/initial-admin-access-key-secret/access-key))

INFRA_URL ?= localhost:8080
.PHONY: create-users
create-users: get-access-key ## Create test users in current dev deployment
	@curl -X POST http://$(INFRA_URL)/api/users \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d '{ "name": "dev@example.com" }' | jq -r '.id'

.PHONY: get-users
get-users: get-access-key ## Get users from current dev deployment
	@curl -s -X GET http://$(INFRA_URL)/api/users \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' | jq -r '.items[]'

.PHONY: get-user
get-user: get-access-key
	$(eval USER_ID:=$(shell curl -s -X GET "http://$(INFRA_URL)/api/users" \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' | jq -r '.items[] | select( .name | contains("dev@example.com") ) | .id'))
	@echo "USER_ID=$(USER_ID)"

.PHONY: create-groups
create-groups: get-access-key ## Create test groups in current dev deployment
	@curl -X POST http://$(INFRA_URL)/api/groups \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d '{ "name": "Example" }' | jq -r '.id'

.PHONY: get-groups
get-groups: get-access-key ## Get groups from current dev deployment
	@curl -s -X GET http://$(INFRA_URL)/api/groups \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' | jq -r '.items[].name'

.PHONY: get-group
get-group: get-access-key
	$(eval GROUP_ID:=$(shell curl -s -X GET http://$(INFRA_URL)/api/groups \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)'  | jq -r '.items[] | select( .name | contains("Example") ) | .id'))
	@echo "GROUP_ID=$(GROUP_ID)"

.PHONY: add-user-group
add-user-group: get-access-key get-group get-user ## Add users to group in current dev deployment
	curl -v -X PATCH http://$(INFRA_URL)/api/groups/$(GROUP_ID)/users \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d "{  \"usersToAdd\": [\"$(USER_ID)\"] }"

.PHONY: add-grants
add-grants: get-access-key ## Add grants to group in current dev deployment
	curl -v -X PATCH http://$(INFRA_URL)/api/grants \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d '{ "grantsToAdd": [{ "groupName": "Example", "privilege": "view", "resource": "production" }] }'

.PHONY: create-destination
create-destination: get-access-key ## Create test destination in current dev deployment
	curl -v -X POST http://$(INFRA_URL)/api/destinations \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d '{ "connection": { "ca": "-----BEGIN CERTIFICATE-----\nMIIDNTCCAh2gAwIBAgIRALRetnpcTo9O3V2fAK3ix+c\n-----END CERTIFICATE-----\n", "url": "aa60eexample.us-west-2.elb.amazonaws.com"}, "kind": "kubernetes", "name": "production" }'

.PHONY: test-data
test-data: create-users create-groups add-user-group create-destination add-grants
