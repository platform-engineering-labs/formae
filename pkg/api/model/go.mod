module github.com/platform-engineering-labs/formae/pkg/api/model

go 1.26

require (
	github.com/platform-engineering-labs/formae/pkg/model v0.0.0-20260120024139-dfbbaa3804a2
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/theory/jsonpath v0.10.2 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/platform-engineering-labs/formae/pkg/model => ../../model
