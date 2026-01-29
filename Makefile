# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

.DEFAULT_GOAL := all

DEBUG_GOFLAGS := -gcflags="all=-N -l"
VERSION := $(shell git describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*")

PKL_BUNDLE_VERSION := 0.30.0
PKL_BIN_URL := https://github.com/apple/pkl/releases/download/${PKL_BUNDLE_VERSION}/pkl-$(shell ./scripts/baduname.sh)

# External plugin Git repositories to bundle
EXTERNAL_PLUGIN_REPOS ?= \
    https://github.com/platform-engineering-labs/formae-plugin-aws.git \
    https://github.com/platform-engineering-labs/formae-plugin-azure.git \
    https://github.com/platform-engineering-labs/formae-plugin-gcp.git \
    https://github.com/platform-engineering-labs/formae-plugin-oci.git \
    https://github.com/platform-engineering-labs/formae-plugin-ovh.git

# Directory for cloned plugins
PLUGINS_CACHE := .plugins

clean:
	rm -rf .out/
	rm -rf dist/
	rm -rf formae
	rm -rf ppm
	rm -rf version.semver
	rm -rf $(PLUGINS_CACHE)
	find ./plugins -name '*.so' -delete

clean-pel:
	rm -rf ~/.pel/*

build:
	go build -C plugins/auth-basic -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o auth-basic.so
	go build -C plugins/pkl -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o pkl.so
	go build -C plugins/json -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o json.so
	go build -C plugins/yaml -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o yaml.so
	go build -C plugins/fake-aws -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o fake-aws.so
	go build -C plugins/tailscale -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o tailscale.so
	go build -ldflags="-X 'github.com/platform-engineering-labs/formae.Version=${VERSION}'" -o formae cmd/formae/main.go

build-tools:
	go build -C ./tools/ppm/cmd -o ../../../ppm

## fetch-external-plugins: Clone/update external plugin repositories
fetch-external-plugins:
	@mkdir -p $(PLUGINS_CACHE)
	@for repo in $(EXTERNAL_PLUGIN_REPOS); do \
		name=$$(basename $$repo .git); \
		if [ -d "$(PLUGINS_CACHE)/$$name" ]; then \
			echo "Updating $$name..."; \
			git -C "$(PLUGINS_CACHE)/$$name" pull --ff-only; \
		else \
			echo "Cloning $$name..."; \
			git clone --depth 1 $$repo "$(PLUGINS_CACHE)/$$name"; \
		fi \
	done

## build-external-plugins: Build all external plugins
build-external-plugins: fetch-external-plugins
	@for repo in $(EXTERNAL_PLUGIN_REPOS); do \
		name=$$(basename $$repo .git); \
		echo "Building $$name..."; \
		$(MAKE) -C "$(PLUGINS_CACHE)/$$name" build; \
	done

## install-external-plugins: Install external plugins to user directory (wipes existing versions)
install-external-plugins: build-external-plugins
	@for repo in $(EXTERNAL_PLUGIN_REPOS); do \
		name=$$(basename $$repo .git); \
		plugin_dir="$(PLUGINS_CACHE)/$$name"; \
		namespace=$$(pkl eval -x 'namespace' "$$plugin_dir/formae-plugin.pkl" | tr '[:upper:]' '[:lower:]'); \
		version=$$(pkl eval -x 'version' "$$plugin_dir/formae-plugin.pkl"); \
		plugin_name=$$(pkl eval -x 'name' "$$plugin_dir/formae-plugin.pkl"); \
		dest="$$HOME/.pel/formae/plugins/$$namespace/v$$version"; \
		echo "Installing resource plugin: $$namespace v$$version to $$dest"; \
		rm -rf "$$HOME/.pel/formae/plugins/$$namespace"; \
		mkdir -p "$$dest/schema"; \
		cp "$$plugin_dir/bin/$$plugin_name" "$$dest/$$namespace"; \
		cp "$$plugin_dir/formae-plugin.pkl" "$$dest/"; \
		cp -r "$$plugin_dir/schema/pkl" "$$dest/schema/"; \
	done
	@echo "External plugins installed successfully."

build-debug:
	go build -C plugins/auth-basic ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o auth-basic-debug.so
	go build -C plugins/pkl ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o pkl-debug.so
	go build -C plugins/json ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o json-debug.so
	go build -C plugins/yaml ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o yaml-debug.so
	go build -C plugins/fake-aws ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o fake-aws-debug.so
	go build -C plugins/tailscale ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o tailscale-debug.so
	go build ${DEBUG_GOFLAGS} -o formae cmd/formae/main.go

pkg-bin: clean build build-tools build-external-plugins
	echo '${VERSION}' > ./version.semver
	mkdir -p ./dist/pel/formae/bin
	mkdir -p ./dist/pel/formae/plugins
	cp -Rp ./formae ./dist/pel/formae/bin
	for f in ./plugins/*/*.so; do \
		if [ -f "$$f" ] && file "$$f" | grep -qE "ELF|Mach-O"; then \
			cp "$$f" ./dist/pel/formae/plugins/; \
		fi \
	done
	rm -f ./dist/pel/formae/plugins/fake-*.so
	# Package external resource plugins
	@for repo in $(EXTERNAL_PLUGIN_REPOS); do \
		name=$$(basename $$repo .git); \
		plugin_dir="$(PLUGINS_CACHE)/$$name"; \
		namespace=$$(pkl eval -x 'namespace' "$$plugin_dir/formae-plugin.pkl" | tr '[:upper:]' '[:lower:]'); \
		version=$$(pkl eval -x 'version' "$$plugin_dir/formae-plugin.pkl"); \
		plugin_name=$$(pkl eval -x 'name' "$$plugin_dir/formae-plugin.pkl"); \
		dest="./dist/pel/formae/resource-plugins/$$namespace/v$$version"; \
		echo "Packaging resource plugin: $$namespace v$$version"; \
		mkdir -p "$$dest/schema"; \
		cp "$$plugin_dir/bin/$$plugin_name" "$$dest/$$namespace"; \
		cp "$$plugin_dir/formae-plugin.pkl" "$$dest/"; \
		cp -r "$$plugin_dir/schema/pkl" "$$dest/schema/"; \
		mkdir -p "./dist/pel/formae/examples/$$plugin_name"; \
		cp -r "$$plugin_dir/examples/"* "./dist/pel/formae/examples/$$plugin_name/" 2>/dev/null || true; \
	done
	curl -L -o ./dist/pel/formae/bin/pkl ${PKL_BIN_URL}
	chmod 755 ./dist/pel/formae/bin/pkl
	./ppm pkg build --name formae --version ${VERSION} ./dist/pel/formae

publish-bin: pkg-bin
	./ppm repo publish ./dist/packages/*.tgz

gen-pkl:
	echo '${VERSION}' > ./version.semver
	pkl project resolve plugins/pkl/schema
	pkl project resolve plugins/pkl/generator
	pkl project resolve plugins/pkl/testdata/forma
	pkl project resolve pkg/plugin/descriptors/

## pkg-pkl: Package core formae schema only
pkg-pkl:
	pkl project package ./plugins/pkl/schema --skip-publish-check

## publish-pkl: Publish core formae schema to S3
publish-pkl:
	aws s3 sync .out/formae@${VERSION} s3://hub.platform.engineering/plugins/pkl/schema/pkl/formae/

## gen-external-pkl: Resolve external plugin PKL schemas (requires formae to be published first)
gen-external-pkl: fetch-external-plugins
	@for repo in $(EXTERNAL_PLUGIN_REPOS); do \
		name=$$(basename $$repo .git); \
		plugin_dir="$(PLUGINS_CACHE)/$$name"; \
		schema_dir="$$plugin_dir/schema/pkl"; \
		if [ -d "$$schema_dir" ] && [ -f "$$schema_dir/PklProject" ]; then \
			version=$$(pkl eval -x 'version' "$$plugin_dir/formae-plugin.pkl"); \
			echo "$$version" > "$$schema_dir/VERSION"; \
			echo "Resolving PKL schema for $$name (v$$version)..."; \
			pkl project resolve "$$schema_dir"; \
		fi \
	done

## pkg-external-pkl: Package external plugin PKL schemas
pkg-external-pkl: gen-external-pkl
	@for repo in $(EXTERNAL_PLUGIN_REPOS); do \
		name=$$(basename $$repo .git); \
		schema_dir="$(PLUGINS_CACHE)/$$name/schema/pkl"; \
		if [ -d "$$schema_dir" ] && [ -f "$$schema_dir/PklProject" ]; then \
			echo "Packaging PKL schema for $$name..."; \
			pkl project package "$$schema_dir" --skip-publish-check; \
		fi \
	done

## publish-external-pkl: Publish external plugin PKL schemas to S3
publish-external-pkl:
	@for repo in $(EXTERNAL_PLUGIN_REPOS); do \
		name=$$(basename $$repo .git); \
		plugin_dir="$(PLUGINS_CACHE)/$$name"; \
		schema_dir="$$plugin_dir/schema/pkl"; \
		if [ -d "$$schema_dir" ] && [ -f "$$schema_dir/PklProject" ]; then \
			plugin_name=$$(pkl eval -x 'name' "$$plugin_dir/formae-plugin.pkl"); \
			version=$$(pkl eval -x 'version' "$$plugin_dir/formae-plugin.pkl"); \
			echo "Publishing PKL schema for $$plugin_name@$$version..."; \
			aws s3 sync ".out/$${plugin_name}@$${version}" \
				"s3://hub.platform.engineering/plugins/$${plugin_name}/schema/pkl/$${plugin_name}/"; \
		fi \
	done

publish-setup:
	aws s3 cp ./scripts/setup.sh s3://hub.platform.engineering/setup/formae.sh

run:
	go run cmd/formae/main.go

test-build:
	@for pkg in $(shell go list ./...); do \
		go test -c -tags="property e2e" "$$pkg" || exit 1; \
		done

test-all: test-build test-pkl
	go test -C ./plugins/auth-basic -tags="unit integration" -count=1 -failfast ./...
	go test -C ./plugins/pkl -tags="unit integration" -count=1 -failfast ./...
	go test -C ./plugins/tailscale -tags="unit integration" -count=1 -failfast ./...
	go test -C ./pkg/model -tags="unit integration" -count=1 -failfast ./
	go test -C ./pkg/plugin -tags="unit integration" -count=1 -failfast ./
	go test -tags="unit integration" -count=1 -failfast ./...

test-unit:
	go test -C ./plugins/auth-basic -tags="unit" -count=1 -failfast ./...
	go test -C ./plugins/pkl -tags=unit -failfast ./...
	go test -C ./plugins/tailscale -tags=unit -failfast ./...
	go test -C ./pkg/model -tags=unit -failfast ./
	go test -C ./pkg/plugin -tags=unit -failfast ./
	go test -tags=unit -failfast ./...

postgres-up:
	docker rm -f formae-test-postgres 2>/dev/null || true
	docker run -d --name formae-test-postgres \
		-e POSTGRES_USER=formae \
		-e POSTGRES_PASSWORD=formae \
		-e POSTGRES_DB=formae \
		-p 5433:5432 \
		postgres:15-alpine

postgres-down:
	docker rm -f formae-test-postgres

test-unit-postgres:
	go test -v -tags=unit -failfast ./internal/metastructure/datastore -args -dbType=postgres

test-unit-summary:
	go test -tags=unit -count=1 -json  ./... | jq 'select(.Action == "fail")'

test-integration:
	go test -C ./plugins/auth-basic -tags=integration -failfast ./...
	go test -C ./plugins/pkl -tags=integration -failfast ./...
	go test -C ./plugins/tailscale -tags=integration -failfast ./...
	go test -tags=integration -failfast ./...

test-e2e: gen-pkl pkg-pkl build install-external-plugins
	echo "Setting up e2e PKL dependencies..."
	bash ./tests/e2e/setup-pkl-deps.sh
	echo "Resolving PKL project..."
	pkl project resolve tests/e2e/pkl
	echo "Running full E2E test suite..."
	bash ./tests/e2e/e2e.sh

test-property:
	go test -tags=property -failfast ./internal/workflow_tests/local -run 'TestMetastructure_Property.*'

test-schema-pkl:
	cd plugins/pkl/schema && pkl test tests/formae.pkl
	cd plugins/pkl/assets && pkl test tests/PklProjectTemplate_test.pkl

test-generator-pkl:
	# Generate static files for local development
	cd plugins/pkl/generator && pkl eval ImportsGenerator.pkl -o imports.pkl
	cd plugins/pkl/generator && pkl eval ResourcesGenerator.pkl -o resources.pkl
	cd plugins/pkl/generator && pkl eval ResolvablesGenerator.pkl -o resolvables.pkl
	cd plugins/pkl/generator/ && pkl test tests/gen.pkl
	# Note: AWS-specific generator examples removed - AWS plugin now external

test-descriptors-pkl:
	pkl test pkg/plugin/descriptors/test/PklProjectGenerator_test.pkl
	pkl test pkg/plugin/descriptors/test/ImportsGenerator_test.pkl

verify-schema-fakeaws:
	cd ./pkg/plugin && go run ./testutil/cmd/verify-schema --namespace fakeaws ../../plugins/fake-aws/schema/pkl

test-pkl: gen-pkl test-schema-pkl test-generator-pkl test-descriptors-pkl

tidy-all:
	go mod tidy
	cd ./plugins/auth-basic && go mod tidy
	cd ./plugins/fake-aws && go mod tidy
	cd ./plugins/json && go mod tidy
	cd ./plugins/yaml && go mod tidy
	cd ./plugins/pkl && go mod tidy
	cd ./plugins/tailscale && go mod tidy
	cd ./tools/ppm && go mod tidy
	cd ./pkg/model && go mod tidy
	cd ./pkg/plugin && go mod tidy
	cd ./pkg/ppm && go mod tidy

version:
	@echo ${VERSION}

api-docs:
	@echo "Generating API documentation..."
	@swag init -d internal/api -g server.go --parseInternal --parseDependency --quiet
	@echo "API documentation generated successfully."

lint:
	@echo "Running linter..."
	@golangci-lint run
	@echo "Linting completed successfully."

lint-reuse:
	./scripts/lint_reuse.sh

add-license:
	./scripts/add_license.sh

all: clean build build-tools gen-pkl api-docs

.PHONY: api-docs clean build build-tools build-debug fetch-external-plugins build-external-plugins install-external-plugins pkg-bin publish-bin gen-pkl gen-external-pkl pkg-pkl pkg-external-pkl publish-pkl publish-external-pkl publish-setup run tidy-all test-build test-all test-unit test-unit-postgres test-unit-summary test-integration test-e2e test-property test-descriptors-pkl verify-schema-fakeaws version full-e2e lint lint-reuse add-license postgres-up postgres-down all
