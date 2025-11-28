module github.com/platform-engineering-labs/formae/plugins/fake-aws
replace ergo.services/actor/statemachine => /home/jeroen/dev/oss/ergo-services/actor/statemachine


go 1.25

toolchain go1.25.1

replace github.com/platform-engineering-labs/formae/pkg/plugin => ../../pkg/plugin

replace github.com/platform-engineering-labs/formae/pkg/model => ../../pkg/model

require (
	github.com/masterminds/semver v1.5.0
	github.com/platform-engineering-labs/formae/pkg/plugin v0.0.0-00010101000000-000000000000
)

require github.com/platform-engineering-labs/formae/pkg/model v0.0.0-00010101000000-000000000000

require (
	ergo.services/actor/statemachine v0.0.0-20250718124030-20d1491f2900 // indirect
	ergo.services/ergo v1.999.310 // indirect
	github.com/PaesslerAG/gval v1.0.0 // indirect
	github.com/PaesslerAG/jsonpath v0.1.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
)
