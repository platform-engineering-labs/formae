module github.com/platform-engineering-labs/formae/plugins/aws

go 1.25

toolchain go1.25.1

replace github.com/platform-engineering-labs/formae/pkg/plugin => ../../pkg/plugin

replace github.com/platform-engineering-labs/formae/pkg/model => ../../pkg/model

require (
	github.com/apple/pkl-go v0.12.0
	github.com/aws/aws-sdk-go-v2 v1.39.3
	github.com/aws/aws-sdk-go-v2/config v1.29.14
	github.com/aws/aws-sdk-go-v2/service/cloudcontrol v1.23.4
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.225.0
	github.com/aws/aws-sdk-go-v2/service/eks v1.74.3
	github.com/aws/aws-sdk-go-v2/service/iam v1.47.3
	github.com/aws/aws-sdk-go-v2/service/route53 v1.49.1
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.36.1
	github.com/aws/aws-sdk-go-v2/service/ssm v1.63.0
	github.com/aws/smithy-go v1.23.1
	github.com/google/uuid v1.6.0
	github.com/masterminds/semver v1.5.0
	github.com/platform-engineering-labs/formae/pkg/model v0.0.0-00010101000000-000000000000
	github.com/platform-engineering-labs/formae/pkg/plugin v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.11.1
	github.com/tidwall/gjson v1.18.0
)

require (
	ergo.services/actor/statemachine v0.0.0-20251202053101-c0aa08b403e5 // indirect
	ergo.services/ergo v1.999.310 // indirect
	github.com/PaesslerAG/gval v1.0.0 // indirect
	github.com/PaesslerAG/jsonpath v0.1.1 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.67 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.10 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.10 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.25.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.30.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.33.19 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
