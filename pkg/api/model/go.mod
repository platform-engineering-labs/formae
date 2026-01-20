module github.com/platform-engineering-labs/formae/pkg/api/model

go 1.25

require github.com/platform-engineering-labs/formae/pkg/model v0.0.0

require (
	github.com/theory/jsonpath v0.10.2 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
)

replace github.com/platform-engineering-labs/formae/pkg/model => ../../model
