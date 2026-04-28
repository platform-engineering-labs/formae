# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

.DEFAULT_GOAL := all

DEBUG_GOFLAGS := -gcflags="all=-N -l"

# The latest tag, possibly carrying a `-channel` suffix (e.g., 0.85.0-dev).
RAW_VERSION := $(shell git describe --tags --abbrev=0 --match "[0-9]*" --match "v[0-9]*")
# Canonical semver — everything before the first `-`. Used as the artifact
# version for binaries and the PKL package. Mirrors the convention already
# in justfile and container.yml.
VERSION := $(shell echo "$(RAW_VERSION)" | cut -d'-' -f1)
# Channel — everything after the first `-`, or `stable` if the tag has no
# suffix. Used for orbital channel routing. PKL schemas are always published
# to a flat URL regardless of channel; channel only affects binary/container
# release routing.
#
# Implemented in pure Make builtins so it parses on GNU make 3.81 (the
# macOS-default in CI) as well as 4.x (Linux). `subst` turns "0.85.0-dev"
# into "0.85.0 dev"; `word 2` extracts "dev". `or` returns the first
# non-empty arg, defaulting to "stable" when there is no `-channel`
# suffix.
CHANNEL := $(or $(word 2,$(subst -, ,$(RAW_VERSION))),stable)

clean:
	rm -rf .out/
	rm -rf dist/
	rm -rf formae
	rm -rf version.semver

clean-pel:
	rm -rf ~/.pel/*

build:
	go build -ldflags="-X 'github.com/platform-engineering-labs/formae.Version=${VERSION}'" -o formae cmd/formae/main.go

## install-gremlins: Install the gremlins mutation testing tool
install-gremlins:
	go install github.com/go-gremlins/gremlins/cmd/gremlins@latest

build-debug:
	go build ${DEBUG_GOFLAGS} -o formae cmd/formae/main.go

pkg-bin: clean build
	echo '${VERSION}' > ./version.semver
	mkdir -p ./dist/pel/bin
	cp -Rp ./formae ./dist/pel/bin

gen-pkl:
	echo '${VERSION}' > ./version.semver
	pkl project resolve internal/schema/pkl/schema
	pkl project resolve internal/schema/pkl/generator
	pkl project resolve internal/schema/pkl/testdata/forma
	pkl project resolve pkg/plugin/descriptors/

## pkg-pkl: Package core formae schema only
pkg-pkl:
	pkl project package ./internal/schema/pkl/schema --skip-publish-check

## publish-pkl: Publish core formae schema to S3
publish-pkl:
	aws s3 sync .out/formae@${VERSION} s3://hub.platform.engineering/plugins/pkl/schema/pkl/formae/

run:
	go run cmd/formae/main.go

test-build:
	@for pkg in $(shell go list ./...); do \
		go test -c -tags="property e2e" "$$pkg" || exit 1; \
		done

test-all: test-build test-pkl
	go test -C ./pkg/auth -tags="unit integration" -count=1 -failfast ./...
	go test -C ./pkg/model -tags="unit integration" -count=1 -failfast ./...
	go test -C ./pkg/plugin -tags="unit integration" -count=1 -failfast ./...
	go test -tags="unit integration" -count=1 -failfast ./...

test-unit:
	go test -C ./pkg/auth -tags=unit -failfast ./...
	go test -C ./pkg/model -tags=unit -failfast ./...
	go test -C ./pkg/plugin -tags=unit -failfast ./...
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

local-data-api-up:
	docker rm -f local-data-api-postgres local-data-api 2>/dev/null || true
	docker network create local-data-api-net 2>/dev/null || true
	docker run -d --name local-data-api-postgres \
		--network local-data-api-net \
		-e POSTGRES_USER=postgres \
		-e POSTGRES_PASSWORD=postgres \
		-e POSTGRES_DB=formae \
		postgres:15-alpine
	sleep 3
	docker run -d --name local-data-api \
		--network local-data-api-net \
		-e ENGINE=PostgreSQLJDBC \
		-e POSTGRES_HOST=local-data-api-postgres \
		-e POSTGRES_PORT=5432 \
		-e POSTGRES_USER=postgres \
		-e POSTGRES_PASSWORD=postgres \
		-e RESOURCE_ARN=arn:aws:rds:us-east-1:123456789012:cluster:local \
		-e SECRET_ARN=arn:aws:secretsmanager:us-east-1:123456789012:secret:local \
		-p 80:80 \
		koxudaxi/local-data-api

local-data-api-down:
	docker rm -f local-data-api local-data-api-postgres 2>/dev/null || true
	docker network rm local-data-api-net 2>/dev/null || true

# CI version: uses --network host to connect to existing PostgreSQL
# Container listens on port 80, so with --network host it binds to host:80
local-data-api-ci:
	docker rm -f local-data-api 2>/dev/null || true
	docker run -d --name local-data-api \
		--network host \
		-e ENGINE=PostgreSQLJDBC \
		-e POSTGRES_HOST=localhost \
		-e POSTGRES_PORT=5432 \
		-e POSTGRES_USER=postgres \
		-e POSTGRES_PASSWORD=admin \
		-e RESOURCE_ARN=arn:aws:rds:us-east-1:123456789012:cluster:local \
		-e SECRET_ARN=arn:aws:secretsmanager:us-east-1:123456789012:secret:local \
		koxudaxi/local-data-api
	@echo "Waiting for local-data-api to be ready..."
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
		if curl -s http://localhost:80/ > /dev/null 2>&1; then \
			echo "local-data-api is ready"; \
			exit 0; \
		fi; \
		echo "Attempt $$i: waiting..."; \
		sleep 2; \
	done; \
	echo "local-data-api failed to start"; \
	docker logs local-data-api; \
	exit 1

test-unit-postgres:
	go test -v -tags=unit -failfast ./internal/datastore/postgres

test-unit-auroradataapi:
	FORMAE_TEST_AURORA_CLUSTER_ARN=arn:aws:rds:us-east-1:123456789012:cluster:local \
	FORMAE_TEST_AURORA_SECRET_ARN=arn:aws:secretsmanager:us-east-1:123456789012:secret:local \
	FORMAE_TEST_AURORA_DATABASE=postgres \
	FORMAE_TEST_AURORA_ENDPOINT=http://localhost:80 \
		go test -v -tags=unit -count=1 -failfast ./internal/datastore/aurora

test-unit-summary:
	go test -tags=unit -count=1 -json  ./... | jq 'select(.Action == "fail")'

test-integration:
	go test -tags=integration -failfast ./...

test-e2e: build
	echo "Setting up e2e PKL dependencies..."
	bash ./tests/e2e/go/setup_pkl.sh
	echo "Staging formae binary in installer-shaped tree (.../bin/formae + .../.ops)..."
	mkdir -p $(CURDIR)/dist/e2e/bin $(CURDIR)/dist/e2e/.ops
	cp $(CURDIR)/formae $(CURDIR)/dist/e2e/bin/formae
	echo "Running e2e tests..."
	E2E_FORMAE_BINARY=$(CURDIR)/dist/e2e/bin/formae go test -C ./tests/e2e/go -tags=e2e -timeout 30m -v ./... $(E2E_RUN_FLAGS)

## test-property: Run property tests (FullChaos 100 iterations, others 50)
test-property:
	go test -C tests/blackbox -tags=property -run 'TestProperty_Sequential|TestProperty_Concurrent' -v -count=1 -rapid.checks=50 -timeout=60m
	go test -C tests/blackbox -tags=property -run TestProperty_FullChaos -v -count=1 -rapid.checks=100 -timeout=60m

## mutation-test: Run mutation testing across all unit-tested packages and generate report
mutation-test: build
	@echo "Running mutation testing (this will take a while)..."
	./scripts/mutation-test.sh
	./scripts/coverage-diff.sh
	./scripts/generate-mutation-report.sh
	@echo "Report: .mutation-report/summary.md"

test-schema-pkl:
	cd internal/schema/pkl/schema && pkl test tests/formae.pkl
	cd internal/schema/pkl/assets && pkl test tests/PklProjectTemplate_test.pkl

test-generator-pkl:
	# Stage 1: Generate intermediate files from PklProject dependencies
	cd internal/schema/pkl/generator && pkl eval ImportsGenerator.pkl -o imports.pkl
	cd internal/schema/pkl/generator && pkl eval ResourcesGenerator.pkl -o resources.pkl
	cd internal/schema/pkl/generator && pkl eval ResolvablesGenerator.pkl -o resolvables.pkl
	# Stage 2: Unit tests
	cd internal/schema/pkl/generator && pkl test tests/gen.pkl
	cd internal/schema/pkl/generator && pkl test tests/jsonhelper.pkl
	# Stage 3: Integration test - generate + validate
	cd internal/schema/pkl/generator && pkl test tests/pklGenerator.pkl
	# Stage 4: Full pipeline validation - generate PKL and evaluate it
	@cd internal/schema/pkl/generator && mkdir -p tmp && \
	for f in examples/json/*.json; do \
		name=$$(basename $$f .json); \
		echo "Testing $$name..."; \
		pkl eval runPklGenerator.pkl \
			-p Json="$$(cat $$f)" > tmp/$$name.pkl && \
		pkl eval tmp/$$name.pkl > /dev/null && \
		echo "  OK" || { echo "  FAILED"; exit 1; }; \
	done

test-descriptors-pkl:
	pkl test pkg/plugin/descriptors/test/PklProjectGenerator_test.pkl
	pkl test pkg/plugin/descriptors/test/ImportsGenerator_test.pkl

test-pkl: gen-pkl test-schema-pkl test-generator-pkl test-descriptors-pkl

tidy-all:
	go mod tidy
	cd ./pkg/auth && go mod tidy
	cd ./pkg/model && go mod tidy
	cd ./pkg/plugin && go mod tidy

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

all: clean build gen-pkl api-docs

.PHONY: api-docs clean build install-gremlins build-debug pkg-bin publish-bin gen-pkl pkg-pkl publish-pkl run tidy-all test-build test-all test-unit test-unit-postgres test-unit-auroradataapi test-unit-summary test-integration test-e2e test-property mutation-test test-descriptors-pkl verify-schema-fakeaws version full-e2e lint lint-reuse add-license postgres-up postgres-down local-data-api-up local-data-api-down all