module github.com/platform-engineering-labs/formae/tests/e2e/go/fixtures/oidc-auth-stub

go 1.26

replace github.com/platform-engineering-labs/formae/pkg/auth => ../../../../../pkg/auth

require github.com/platform-engineering-labs/formae/pkg/auth v0.0.0
