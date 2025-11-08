module github.com/platform-engineering-labs/formae/plugins/pkl

go 1.25

toolchain go1.25.1

require (
	github.com/apple/pkl-go v0.12.0
	github.com/masterminds/semver v1.5.0
	github.com/platform-engineering-labs/formae/pkg/model v0.0.0-00010101000000-000000000000
	github.com/platform-engineering-labs/formae/pkg/plugin v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.11.1
	github.com/tidwall/gjson v1.18.0
)

require (
	github.com/PaesslerAG/gval v1.0.0 // indirect
	github.com/PaesslerAG/jsonpath v0.1.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/crypto v0.42.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/platform-engineering-labs/formae/pkg/plugin => ../../pkg/plugin

replace github.com/platform-engineering-labs/formae/pkg/model => ../../pkg/model
