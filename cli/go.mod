module github.com/greedylabs/ncloud-community-sdk/cli

go 1.24.0

require (
	github.com/google/uuid v1.6.0
	github.com/greedylabs/ncloud-community-sdk/sdk v0.0.0-monorepo
	github.com/oapi-codegen/runtime v1.4.0
	github.com/spf13/cobra v1.8.1
	golang.org/x/term v0.27.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sys v0.39.0 // indirect
)

// Local monorepo replacement — sdk lives in the sibling ../sdk directory.
// External consumers (go install) won't see this; tagged releases publish
// the SDK as a versioned module and the require directive resolves normally.
replace github.com/greedylabs/ncloud-community-sdk/sdk => ../sdk
