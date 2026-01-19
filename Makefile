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

VM_NAME ?= ssh01
VM_MEM ?= 8192mb
VM_IMAGE ?= gcr.io/k8s-minikube/kicbase:v0.0.46
INFRA_SERVER_IP_SUFFIX ?= 240
INFRA_UI_IP_SUFFIX ?= 241
POSTGRES_IP_SUFFIX ?= 243
SSH_IP_SUFFIX ?= 244
INFRA_ADDR_RANGE ?= 192.168.89
INFRA_IP_ADDR ?= $(INFRA_ADDR_RANGE).$(INFRA_SERVER_IP_SUFFIX)
# INFRA_IP_ADDR ?= $(SERVER_DEV)
INFRA_UI_IP_ADDR ?= $(INFRA_ADDR_RANGE).$(INFRA_UI_IP_SUFFIX)
POSTGRES_IP_ADDR ?= $(INFRA_ADDR_RANGE).$(POSTGRES_IP_SUFFIX)
SSH_IP_ADDR ?= $(INFRA_ADDR_RANGE).$(SSH_IP_SUFFIX)
INFRA_NETWORK ?= infra
NETWORK_ARGS ?= --network=$(INFRA_NETWORK) --add-host=infra.local.net:$(INFRA_IP_ADDR) --add-host=infra-ui.local.net:$(INFRA_UI_IP_ADDR) --add-host=postgres-dev.local.net:$(POSTGRES_IP_ADDR) --add-host=$(VM_NAME).local.net:$(SSH_IP_ADDR)
INFRA_SERVER_URL ?= $(INFRA_IP_ADDR):9443

DOCKER_ENGINE ?= docker
DOCKER_CONTEXT ?= .
REPOSITORY_USER ?= ska-telescope
REPOSITORY_NAME ?= external/infra
DOCKER_HOST ?= registry.gitlab.com
DOCKER_REGISTRY ?= $(DOCKER_HOST)/$(REPOSITORY_USER)/$(REPOSITORY_NAME)
TAG ?= 0.21.8
# BUILDVERSION is for client side compatibility - fixed to 0.21.0
BUILDVERSION ?= 0.21.0
GITLAB_TOKEN ?=

LINT_ARGS ?= --fix

# define overides for above variables in here
-include PrivateRules.mak


clean: clean-oci clean-secrets

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

GO_BUILD_LDFLAGS ?= -s -X github.com/infrahq/infra/internal.Version="v$(BUILDVERSION)" \
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
	RELEASE_NAME=v$(TAG) BUILDVERSION=$(BUILDVERSION) goreleaser release --snapshot --clean

release-artefacts: ## build the release artefacts and publish ti gitlab
	rm -rf dist
	RELEASE_NAME=v$(TAG) BUILDVERSION=$(BUILDVERSION) GITLAB_TOKEN=$(GITLAB_TOKEN) goreleaser release --verbose --clean --skip announce,validate

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

load: docker-push
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
	kubectl config use-context minikube || true

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

dev/connector: dev/context docker-build load
	kubectl create ns infra || true
	helm upgrade infra ./charts/infra -n infra --install --wait $(HELM_FLAGS) \
		--set-string image.pullPolicy=Never \
		--set-string image.repository=$(DOCKER_REGISTRY)/infra \
		--set-string image.tag=$(TAG) \
		--set-string podAnnotations.checksum=$$($(DOCKER_ENGINE) images -q $(DOCKER_REGISTRY)/infra:$(TAG)) \
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
	make infra-network
	make postgres
	sleep 5
	make infra-ui
	make infra-server
	sleep 5
	make test-data
	make ssh-vms
	@echo "Password: $$(cat $(CONF_DIR)/initial-admin-password-secret/password)"

K8S_CONNECTOR_NAME ?= minikube-k8s
.PHONY: k8s-connector-key
k8s-connector-key:
	rm -f /tmp/connector_key.txt
	INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) dist/infra_linux_amd64_v1/infra login $(INFRA_SERVER_URL) --skip-tls-verify
	INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) dist/infra_linux_amd64_v1/infra keys remove $(K8S_CONNECTOR_NAME) --connector --force || true
	INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) dist/infra_linux_amd64_v1/infra keys add --connector --name $(K8S_CONNECTOR_NAME) -q > /tmp/connector_key.txt
	INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) dist/infra_linux_amd64_v1/infra logout

define INFRA_HELM_VALUES
service:
  type: LoadBalancer

image:
  repository: registry.gitlab.com/ska-telescope/external/infra/infra
  tag: 0.21.0
  pullPolicy: IfNotPresent

config:
  accessKey: ${CONNECTOR_KEY}
  name: ${K8S_CONNECTOR_NAME}
  server:
    url: https://$${INFRA_SERVER_URL}
    skipTLSVerify: true
    trustedCertificate: |
endef
export INFRA_HELM_VALUES


.PHONY: un-dev-connector
un-dev-connector: dev/context
	helm -n infra uninstall infra-server || true
	helm -n infra uninstall infra || true
	kubectl delete ns infra || true

.PHONY: dev-connector
dev-connector:  un-dev-connector get-access-key k8s-connector-key ## deploy k8s connector integrated with dev-oci environment
	$(eval CONNECTOR_KEY:=$(shell cat /tmp/connector_key.txt))
	@echo "CONNECTOR_KEY=$(CONNECTOR_KEY)"
	@export INFRA_SERVER_URL="$(INFRA_ADDR_RANGE).1:9443"; \
	    echo "$${INFRA_HELM_VALUES}" | envsubst > /tmp/connector-values.yaml
	@cat internal/server/testdata/pki/ca.crt | sed -e 's/^/      /' >> /tmp/connector-values.yaml
	cat /tmp/connector-values.yaml
	make dev/connector HELM_FLAGS="--values /tmp/connector-values.yaml"
	@rm -f /tmp/connector-values.yaml
	@rm -f /tmp/connector_key.txt

.PHONY: postgres
postgres: ## deploy posgres container
	$(DOCKER_ENGINE) run -d --name=postgres-dev --rm \
    	$(NETWORK_ARGS) \
    	--ip $(POSTGRES_IP_ADDR) \
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
clean-oci: clean-infra clean-infra-ui clean-postgres ssh-vms-clean clean-infra-network

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

define INFRA_SERVER_CONF
---
version: 0.2
addr:
  http: :8080
  https: :9443
  metrics: :9090
admin:
  # accessKeySecret: infra-admin-access-key
  enable: true
  enabled: true
# dbEncryptionKey: /home/infra/internal/server/testdata/encryption-key/key
dbHost: ${POSTGRES_IP_ADDR}
dbName: infra
dbParameters:
dbPassword: infra
dbPort: 5432
dbUsername: infra
enableTelemetry: true
logLevel: debug
sessionDuration: 720h0m0s
sessionExtensionDeadline: 72h0m0s
tls:
  ca: /home/infra/internal/server/testdata/pki/ca.crt
  caPrivateKey: /home/infra/internal/server/testdata/pki/ca.key
ui:
  proxyURL: http://${INFRA_UI_IP_ADDR}:3000
users:
  - accessKey: file:/home/infra/internal/server/testdata/initial-admin-access-key-secret/access-key
    infraRole: admin
    name: admin@local
    password: file:/home/infra/internal/server/testdata/initial-admin-password-secret/password
  - infraRole: admin
    name: dev@local
    password: "${INFRA_PASSWORD}"
  - infraRole: view
    name: test01@local.net
    password: "${INFRA_PASSWORD}"
endef
export INFRA_SERVER_CONF

.PHONY: infra-server
infra-server: ## deploy infra server container
	$(DOCKER_ENGINE) rm -f infra || true
	echo "$${INFRA_SERVER_CONF}" | envsubst > $$(pwd)/internal/server/testdata/infra.yaml


	$(DOCKER_ENGINE) run -d --name=infra \
		-e INFRA_SERVER_DB_PASSWORD=infra \
		-v $$(pwd)/internal:/home/infra/internal \
		$(NETWORK_ARGS) \
		--ip $(INFRA_IP_ADDR) \
		-p 8080:8080 \
		-p 9443:9443 \
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
	    $(NETWORK_ARGS) \
    	--ip $(INFRA_UI_IP_ADDR) \
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

USER_NAME ?= dev@example.com
.PHONY: get-user
get-user: get-access-key
	$(eval USER_ID:=$(shell curl -s -X GET "http://$(INFRA_URL)/api/users" \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' | jq -r '.items[] | select( .name | contains("$(USER_NAME)") ) | .id'))
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
	curl -v -X PATCH http://$(INFRA_URL)/api/grants \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d '{ "grantsToAdd": [{ "groupName": "Example", "privilege": "connect", "resource": "ssh01" }] }'
	curl -v -X PATCH http://$(INFRA_URL)/api/grants \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d '{ "grantsToAdd": [{ "userName": "test01@local.net", "privilege": "connect", "resource": "ssh01" }] }'


.PHONY: create-destination
create-destination: get-access-key ## Create test destination in current dev deployment
	curl -v -X POST http://$(INFRA_URL)/api/destinations \
	  -H 'Content-Type: application/json' \
	  -H 'Infra-Version: 0.18.1' \
	  -H 'Authorization: Bearer $(INFRA_ACCESS_KEY)' \
	  -d '{ "connection": { "ca": "-----BEGIN CERTIFICATE-----\nMIIDNTCCAh2gAwIBAgIRALRetnpcTo9O3V2fAK3ix+c\n-----END CERTIFICATE-----\n", "url": "aa60eexample.us-west-2.elb.amazonaws.com"}, "kind": "kubernetes", "name": "production" }'

.PHONY: test-data
test-data: create-users create-groups add-user-group create-destination add-grants
	make add-user-group USER_NAME=test01@local.net

define INFRA_SSHD_CONFIG
Match group infra-users
    AuthorizedKeysFile none
    PasswordAuthentication no
    AllowTcpForwarding yes
    PermitTunnel yes
    AllowAgentForwarding yes
    AuthorizedKeysCommand /usr/local/sbin/infra sshd auth-keys %u %f
    AuthorizedKeysCommandUser infra
endef
export INFRA_SSHD_CONFIG

define INFRA_SYSTEMD
[Unit]
Description=Infra SSH Connector
Documentation=https://confluence.skatelescope.org/display/SWSI/InfraHQ+Management+and+Operation
Wants=network-online.service
After=network-online.service
ConditionPathExists=/etc/infra/connector.yaml

[Service]
Type=simple
ExecStart=/usr/local/sbin/infra connector -f /etc/infra/connector.yaml
Restart=on-failure

[Install]
WantedBy=multi-user.target
endef
export INFRA_SYSTEMD

define INFRA_SSH_CLIENT_CONFIG
kind: ssh
name: "${VM_NAME}"
endpointAddr: ${SSH_IP_ADDR}
server:
  url: https://$(INFRA_IP_ADDR):9443
  accessKey: XXCONNECTOR_KEYXX
endef
export INFRA_SSH_CLIENT_CONFIG

.PHONY: vm-clean
vm-clean: ## Remove a kicbase container that emulates a VM
	$(DOCKER_ENGINE) rm -f $(VM_NAME) || true
	make vm-clean-hosts VM_NAME=$(VM_NAME)

.PHONY: vm-ssh
vm-ssh:
	ssh root@$$($(DOCKER_ENGINE) inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $(VM_NAME))

.PHONY: vm-logs
vm-logs:
		$(DOCKER_ENGINE) exec -ti $(VM_NAME) bash -c "journalctl -f"

.PHONY: vm-create
vm-create: ## Create a kicbase container to emulate a VM
	make vm-clean
	sleep 1
	$(DOCKER_ENGINE) run --rm \
	-d -t \
	--privileged \
	--security-opt seccomp=unconfined \
	--tmpfs /tmp \
	--tmpfs /run \
	--volume /lib/modules:/lib/modules:ro \
	--hostname $(VM_NAME) \
	--ip $(SSH_IP_ADDR) \
	$(NETWORK_ARGS) \
	--name $(VM_NAME) \
	--memory=$(VM_MEM) \
	-e container=$(DOCKER_ENGINE) \
	$(VM_IMAGE)
	sleep 3
	# setup ssh keys
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) bash -c "mkdir -p /root/.ssh; touch /root/.ssh/authorized_keys; chmod 600 /root/.ssh/authorized_keys"
	ssh-add -L | $(DOCKER_ENGINE) exec -i $(VM_NAME) bash -c "cat - >>/root/.ssh/authorized_keys"
	# get rid of bad repos
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) bash -c "rm -f /etc/apt/sources.list.d/devel* /etc/apt/sources.list.d/dock*  /etc/apt/sources.list.d/nvidia*"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) bash -c "sed -i 's/archive/uk.archive/' /etc/apt/sources.list "
	make vm-hosts VM_NAME=$(VM_NAME)

clean-infra-network: # delete infra network
	sudo docker network rm $(INFRA_NETWORK) || true

infra-network: # reconfigure infra network to /24
	sudo docker network create --subnet $(INFRA_ADDR_RANGE).0/24 --driver bridge $(INFRA_NETWORK) || true

.PHONY: infra-ssh-connector-key
infra-ssh-connector-key: get-access-key
	$(DOCKER_ENGINE) cp internal/server/testdata/pki/ca.crt $(VM_NAME):/usr/local/share/ca-certificates/infra-ca.crt
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chown root:root /usr/local/share/ca-certificates/infra-ca.crt"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "update-ca-certificates"
	$(DOCKER_ENGINE) cp dist/infra_linux_amd64_v1/infra $(VM_NAME):/usr/bin/infra
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chown root:root /usr/bin/infra"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) infra login $(INFRA_SERVER_URL) --skip-tls-verify"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) infra keys remove $(VM_NAME) --connector --force || true"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) infra keys add --connector --name $(VM_NAME) -q" > /tmp/connector_key.txt
	$(eval CONNECTOR_KEY:=$(shell cat /tmp/connector_key.txt))
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "INFRA_SERVER=$(INFRA_SERVER_URL) INFRA_ACCESS_KEY=$(INFRA_ACCESS_KEY) infra logout"

.PHONY: infra-ssh-config
infra-ssh-config: infra-ssh-connector-key
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "mkdir -p /usr/local/sbin"
	$(DOCKER_ENGINE) cp dist/infra_linux_amd64_v1/infra $(VM_NAME):/usr/local/sbin/infra
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chown root:root /usr/local/sbin/infra"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chmod 755 /usr/local/sbin/infra"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "addgroup infra; addgroup infra-users"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "adduser --home /etc/infra --shell /usr/sbin/nologin --disabled-password --gecos 'Infra Agent' --ingroup infra infra"
	echo "$${INFRA_SYSTEMD}" > /tmp/infra.service
	$(DOCKER_ENGINE) cp /tmp/infra.service $(VM_NAME):/lib/systemd/system/infra.service
	rm -f /tmp/infra.service
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chown root:root /lib/systemd/system/infra.service"
	echo "$${INFRA_SSHD_CONFIG}" > /tmp/infra.conf
	$(DOCKER_ENGINE) cp /tmp/infra.conf $(VM_NAME):/etc/ssh/sshd_config.d/infra.conf
	rm -f /tmp/infra.conf
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chown root:root /etc/ssh/sshd_config.d/infra.conf"
	export CONNECTOR_KEY=`cat /tmp/connector_key.txt` ; \
	echo "$${INFRA_SSH_CLIENT_CONFIG}" | envsubst | sed "s/XXCONNECTOR_KEYXX/$$CONNECTOR_KEY/" > /tmp/connector.yaml
	rm -rf /tmp/connector_key.txt
	cat /tmp/connector.yaml
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "mkdir -p /etc/infra"
	$(DOCKER_ENGINE) cp /tmp/connector.yaml $(VM_NAME):/etc/infra/connector.yaml
	rm -f /tmp/connector.yaml
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chown root:root /etc/infra/connector.yaml"
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chmod 644 /etc/infra/connector.yaml "
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "systemctl daemon-reload; systemctl restart ssh "
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "chmod 644 /lib/systemd/system/infra.service; systemctl daemon-reload; systemctl restart infra "

.PHONY: ssh-vms
ssh-vms:
	make infra-network
	make vm-create
	# make ssh-vms-hosts
	make infra-ssh-config

.PHONY: ssh-vms-hosts
ssh-vms-hosts: vm-hosts
	IPADDR=`$(DOCKER_ENGINE) inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $(VM_NAME)`; \
	$(DOCKER_ENGINE) exec -ti infra sh -c "echo -e '$$IPADDR $(VM_NAME) $(VM_NAME).local.net\n' >> /etc/hosts "; \
	$(DOCKER_ENGINE) exec -ti $(VM_NAME) sh -c "echo -e '$$IPADDR $(VM_NAME) $(VM_NAME).local.net\n' >> /etc/hosts ";

.PHONY: ssh-vms-clean
ssh-vms-clean: vm-clean-hosts ## clean up ssh VM
	make vm-clean VM_NAME=ssh01


.PHONY: vm-hosts
vm-hosts: vm-clean-hosts
	$(eval IPADDR:=$(shell $(DOCKER_ENGINE) inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $(VM_NAME)))
	sudo echo -e "$(IPADDR) $(VM_NAME) $(VM_NAME).local.net\n" | sudo tee -a /etc/hosts
	tail /etc/hosts
	sudo systemctl daemon-reload
	sudo systemctl restart dnsmasq

.PHONY: vm-clean-hosts
vm-clean-hosts:
	sudo perl -i -ne 'print unless $$_ =~ /$(VM_NAME).local.net/' /etc/hosts
	sudo perl -i -ne 'print unless ($$_ =~ /^\n$$/ and $$e); $$e = ($$_ =~ /^\n$$/ ? "y" : "")' /etc/hosts


PI_HOLE_PASSWD := letmein
infra-hosts-get:
	SID=`curl -s -X POST http://192.168.178.27/api/auth --data '{"password":"$(PI_HOLE_PASSWD)"}' | jq -r .session.sid`; \
	 curl -s -X GET http://192.168.178.27/api/config?sid=$$SID | jq .config.dns.hosts; \
	 curl -s -X GET http://192.168.178.27/api/config/dns/hosts?sid=$$SID | json_pp

infra-hosts-delete:
# have to use patch to delete
	SID=`curl -s -X POST http://192.168.178.27/api/auth --data '{"password":"$(PI_HOLE_PASSWD)"}' | jq -r .session.sid`; \
	 curl -v -X PATCH http://192.168.178.27/api/config/dns/hosts?sid=$$SID --data '{"config":{"dns":{"hosts":[]}}}'

PI_HOLE_PATCH := /tmp/pi.hole.patch
infra-hosts-patch:
# have to use patch to delete
	make infra-hosts-get
	@echo -e '{' > $(PI_HOLE_PATCH)
	@echo -e '"config": {' >> $(PI_HOLE_PATCH)
	@echo -e '"dns": {' >> $(PI_HOLE_PATCH)
	@echo -e '"hosts": [' >> $(PI_HOLE_PATCH)
	@for i in `docker ps --format='{{.Names}}' | grep infra`; do \
	IPADDR=`$(DOCKER_ENGINE) inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $$i`; \
	echo -e "\"$$IPADDR $$i $$i.local.net\"," >> $(PI_HOLE_PATCH); \
	done
	@perl -0777 -i -ne 's/(.*),$$/$$1/;print' $(PI_HOLE_PATCH)
	@echo -e ']}}}' >> $(PI_HOLE_PATCH)
	@cat $(PI_HOLE_PATCH) | json_pp
	SID=`curl -s -X POST http://192.168.178.27/api/auth --data '{"password":"$(PI_HOLE_PASSWD)"}' | jq -r .session.sid`; \
	 curl -v -X PATCH http://192.168.178.27/api/config/dns/hosts?sid=$$SID --data @$(PI_HOLE_PATCH)
	@echo ""
	make infra-hosts-get
