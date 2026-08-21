module github.com/platform-engineering-labs/formae/pkg/credential

go 1.26

toolchain go1.26.2

require (
	ergo.services/ergo v1.999.320
	github.com/klauspost/compress v1.18.5
	github.com/stretchr/testify v1.11.1
	github.com/vmihailenco/msgpack/v5 v5.4.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace ergo.services/ergo => github.com/JeroenSoeters/ergo v1.999.320-pel.6
