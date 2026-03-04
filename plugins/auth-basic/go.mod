module github.com/platform-engineering-labs/formae/plugins/auth-basic

go 1.25

toolchain go1.25.1

replace github.com/platform-engineering-labs/formae/pkg/auth => ../../pkg/auth

require (
	github.com/platform-engineering-labs/formae/pkg/auth v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.47.0
)
