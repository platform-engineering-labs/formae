module github.com/platform-engineering-labs/formae/pkg/plugin

go 1.25

toolchain go1.25.1

replace github.com/platform-engineering-labs/formae/pkg/model => ../model

require (
	github.com/masterminds/semver v1.5.0
	github.com/platform-engineering-labs/formae/pkg/model v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.11.1
	github.com/tidwall/gjson v1.18.0
	golang.org/x/crypto v0.42.0
)

require (
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/PaesslerAG/gval v1.0.0 // indirect
	github.com/PaesslerAG/jsonpath v0.1.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
