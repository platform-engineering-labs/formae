module github.com/platform-engineering-labs/formae/tests/e2e/go/fixtures/oidc-credential-stub

go 1.26

replace github.com/platform-engineering-labs/formae/pkg/credential => ../../../../../pkg/credential

// Match the credential SDK and the agent: both run the forked ergo.
replace ergo.services/ergo => github.com/JeroenSoeters/ergo v1.999.320-pel.6

require github.com/platform-engineering-labs/formae/pkg/credential v0.0.0

require (
	ergo.services/ergo v1.999.320 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
)
