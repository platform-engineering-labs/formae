module github.com/platform-engineering-labs/formae/pkg/plugin-conformance-tests

go 1.25

require (
	ergo.services/ergo v1.999.310
	github.com/platform-engineering-labs/formae/pkg/api/model v0.1.1
	github.com/platform-engineering-labs/formae/pkg/model v0.1.2
	github.com/platform-engineering-labs/formae/pkg/plugin v0.1.7
	github.com/tidwall/gjson v1.18.0
	github.com/tidwall/sjson v1.2.5
)

require (
	ergo.services/actor/statemachine v0.0.0-20251202053101-c0aa08b403e5 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.3 // indirect
	github.com/masterminds/semver v1.5.0 // indirect
	github.com/theory/jsonpath v0.10.2 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.39.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.39.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.39.0 // indirect
	go.opentelemetry.io/otel/metric v1.39.0 // indirect
	go.opentelemetry.io/otel/sdk v1.39.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.39.0 // indirect
	go.opentelemetry.io/otel/trace v1.39.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/grpc v1.77.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

replace (
	github.com/platform-engineering-labs/formae/pkg/api/model => ../api/model
	github.com/platform-engineering-labs/formae/pkg/model => ../model
	github.com/platform-engineering-labs/formae/pkg/plugin => ../plugin
)
