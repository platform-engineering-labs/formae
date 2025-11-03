# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

.DEFAULT_GOAL := all

DEBUG_GOFLAGS := -gcflags="all=-N -l"
VERSION := $(shell git describe --tags --abbrev=0)

PKL_BUNDLE_VERSION := 0.30.0
PKL_BIN_URL := https://github.com/apple/pkl/releases/download/${PKL_BUNDLE_VERSION}/pkl-$(shell ./scripts/baduname.sh)

clean:
	rm -rf .out/
	rm -rf dist/
	rm -rf formae
	rm -rf ppm
	rm -rf version.semver
	find ./plugins -name '*.so' -delete

clean-pel:
	rm -rf ~/.pel/*

build:
	go build -C plugins/auth-basic -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o auth-basic.so
	go build -C plugins/aws -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o aws.so
	go build -C plugins/pkl -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o pkl.so
	go build -C plugins/json -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o json.so
	go build -C plugins/yaml -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o yaml.so
	go build -C plugins/fake-aws -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o fake-aws.so
	go build -C plugins/tailscale -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o tailscale.so
	go build -ldflags="-X 'github.com/platform-engineering-labs/formae.Version=${VERSION}'" -o formae cmd/formae/main.go

build-tools:
	go build -C ./tools/ppm/cmd -o ../../../ppm

build-aws:
	go build -C plugins/aws -buildmode=plugin -o aws.so
	go build -C plugins/aws ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o aws-debug.so

build-debug:
	go build -C plugins/auth-basic ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o auth-basic-debug.so
	go build -C plugins/aws ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o aws-debug.so
	go build -C plugins/pkl ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o pkl-debug.so
	go build -C plugins/json ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o json-debug.so
	go build -C plugins/yaml ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o yaml-debug.so
	go build -C plugins/fake-aws ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o fake-aws-debug.so
	go build -C plugins/tailscale ${DEBUG_GOFLAGS} -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o tailscale-debug.so
	go build ${DEBUG_GOFLAGS} -o formae cmd/formae/main.go

build-pkl-local:
	go build -C plugins/pkl -tags local -ldflags="-X 'main.Version=${VERSION}'" -buildmode=plugin -o pkl.so
	pkl project resolve plugins/pkl/generator/

pkg-bin: clean build build-tools
	echo '${VERSION}' > ./version.semver
	mkdir -p ./dist/pel/formae/bin
	mkdir -p ./dist/pel/formae/plugins
	mkdir -p ./dist/pel/formae/examples
	cp -Rp ./formae ./dist/pel/formae/bin
	find ./plugins -name '*.so' -exec cp {} ./dist/pel/formae/plugins/ \;
	rm -rf ./dist/pel/formae/plugins/fake-*
	cp -Rp ./examples/* ./dist/pel/formae/examples
	./formae project init ./dist/pel/formae/examples
	rm -rf ./dist/pel/formae/examples/main.pkl
	rm -rf ./dist/pel/formae/examples/PklProject.deps.json
	curl -L -o ./dist/pel/formae/bin/pkl ${PKL_BIN_URL}
	chmod 755 ./dist/pel/formae/bin/pkl
	./ppm pkg build --name formae --version ${VERSION} ./dist/pel/formae

publish-bin: pkg-bin
	./ppm repo publish ./dist/packages/*.tgz

gen-pkl:
	echo '${VERSION}' > ./version.semver
	pkl project resolve plugins/pkl/schema
	pkl project resolve plugins/pkl/schema
	pkl project resolve plugins/pkl/generator
	pkl project resolve plugins/aws/schema/pkl
	pkl project resolve plugins/pkl/testdata/forma
	pkl project resolve plugins/pkl/verify
	pkl project resolve examples/
	pkl project resolve tests/e2e/pkl

gen-aws-pkl-types:
	pkl project resolve plugins/aws
	pkl project resolve plugins/aws/pkg/descriptors/pkl
	cd plugins/aws && go generate .
	rm plugins/aws/pkg/descriptors/gen/Types.pkl.go
	cd plugins/aws &&  pkl eval pkg/descriptors/pkl/resources.pkl > pkg/descriptors/pkl/generated_resources.pkl
	@if [ "$$(uname)" = "Darwin" ]; then \
		sed -i '' '/pkl.RegisterStrictMapping("types", Types{})/d' plugins/aws/pkg/descriptors/gen/init.pkl.go; \
	else \
		sed -i '/pkl.RegisterStrictMapping("types", Types{})/d' plugins/aws/pkg/descriptors/gen/init.pkl.go; \
	fi

pkg-pkl:
	pkl project package ./plugins/aws/schema/pkl ./plugins/pkl/schema --skip-publish-check

publish-pkl:
	aws s3 sync .out/aws@${VERSION} s3://hub.platform.engineering/plugins/aws/schema/pkl/aws/
	aws s3 sync .out/formae@${VERSION} s3://hub.platform.engineering/plugins/pkl/schema/pkl/formae/

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
	go test -C ./plugins/aws -tags="unit" -count=1 -failfast ./...
	go test -C ./plugins/azure -tags="unit integration" -count=1 -failfast ./...
	go test -C ./plugins/pkl -tags="unit integration" -count=1 -failfast ./...
	go test -C ./plugins/tailscale -tags="unit integration" -count=1 -failfast ./...
	go test -C ./pkg/model -tags="unit integration" -count=1 -failfast ./
	go test -C ./pkg/plugin -tags="unit integration" -count=1 -failfast ./
	go test -tags="unit integration" -count=1 -failfast ./...

test-unit:
	go test -C ./plugins/auth-basic -tags="unit" -count=1 -failfast ./...
	go test -C ./plugins/aws -tags=unit -failfast ./...
	go test -C ./plugins/pkl -tags=unit -failfast ./...
	go test -C ./plugins/tailscale -tags=unit -failfast ./...
	go test -C ./pkg/model -tags=unit -failfast ./
	go test -C ./pkg/plugin -tags=unit -failfast ./
	go test -tags=unit -failfast ./...

test-unit-postgres:
	go test -v -tags=unit -failfast ./internal/metastructure/datastore -args -dbType=postgres

test-unit-summary:
	go test -tags=unit -count=1 -json  ./... | jq 'select(.Action == "fail")'

test-integration:
	go test -C ./plugins/auth-basic -tags=integration -failfast ./...
	go test -C ./plugins/aws -tags=integration -failfast ./...
	go test -C ./plugins/azure -tags=integration -failfast ./...
	go test -C ./plugins/pkl -tags=integration -failfast ./...
	go test -C ./plugins/tailscale -tags=integration -failfast ./...
	go test -tags=integration -failfast ./...

test-plugin-sdk-aws: build
	@echo "Resolving PKL dependencies for AWS plugin..."
	@pkl project resolve plugins/aws/testdata
	PLUGIN_NAME=aws go test -C ./tests/integration/plugin-sdk -tags=plugin_sdk -v -failfast ./...

test-plugin-sdk-azure: build
	@echo "Resolving PKL dependencies for Azure plugin..."
	@pkl project resolve plugins/azure/testdata
	PLUGIN_NAME=azure go test -C ./tests/integration/plugin-sdk -tags=plugin_sdk -v -failfast ./...

test-e2e: gen-pkl pkg-pkl build
	echo "Resolving PKL project..."
	pkl project resolve tests/e2e/pkl
	echo "Running full E2E test suite..."
	echo "Backing up existing config file if it exists..."
	@set -e; \
		mkdir -p "$$HOME/.config/formae"
	if [ -f "$$HOME/.config/formae/formae.conf.pkl" ]; then \
		cp "$$HOME/.config/formae/formae.conf.pkl" "$$HOME/.config/formae/formae.conf.pkl.backup"; \
		echo "Existing config backed up to formae.conf.pkl.backup"; \
		fi; \
		echo "Copy over test config file"; \
		cp tests/e2e/config/formae.conf.pkl "$$HOME/.config/formae/formae.conf.pkl"; \
		echo "Running E2E tests..."; \
		if bash ./tests/e2e/e2e.sh; then \
		echo "E2E tests completed successfully"; \
		E2E_EXIT_CODE=0; \
		else \
		echo "E2E tests failed"; \
		E2E_EXIT_CODE=1; \
		fi; \
		echo "Restoring original config file..."; \
		if [ -f "$$HOME/.config/formae/formae.conf.pkl.backup" ]; then \
		mv "$$HOME/.config/formae/formae.conf.pkl.backup" "$$HOME/.config/formae/formae.conf.pkl"; \
		echo "Original config restored"; \
		else \
		rm -f "$$HOME/.config/formae/formae.conf.pkl"; \
		echo "Test config removed (no original config to restore)"; \
		fi; \
		exit $$E2E_EXIT_CODE

test-property:
	go test -tags=property -failfast ./internal/workflow_tests -run 'TestMetastructure_Property.*'

test-schema-pkl:
	cd plugins/pkl/schema && pkl test tests/formae.pkl

test-generator-pkl:
	cd plugins/pkl/generator/ && pkl test tests/gen.pkl
	cd plugins/pkl/generator/ && pkl eval runLocalPklGenerator.pkl -p File=./examples/json/resources_example.json
	cd plugins/pkl/generator/ && pkl eval runLocalPklGenerator.pkl -p File=./examples/json/lifeline.json
	cd plugins/pkl/generator && python3 run_generator.py examples/json/types

verify-pkl: gen-pkl
	cd plugins/pkl/verify && pkl eval verify.pkl

test-pkl: gen-pkl test-schema-pkl test-generator-pkl

tidy-all:
	go mod tidy
	cd ./plugins/auth-basic && go mod tidy
	cd ./plugins/aws && go mod tidy
	cd ./plugins/azure && go mod tidy
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
	@golangci-lint-v2 run
	@echo "Linting completed successfully."

lint-reuse:
	./scripts/lint_reuse.sh

add-license:
	./scripts/add_license.sh

all: clean build build-tools gen-pkl api-docs

.PHONY: api-docs clean build build-tools build-aws build-debug build-pkl-local pkg-bin publish-bin gen-pkl gen-aws-pkl-types pkg-pkl publish-pkl publish-setup run tidy-all test-build test-all test-unit test-unit-summary test-integration test-e2e test-property version full-e2e lint lint-reuse add-license all
