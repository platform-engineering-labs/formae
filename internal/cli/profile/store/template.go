// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package store

// StubTemplate is the minimal, commented PKL config written by `formae profile
// create` and by clean-install bootstrap. It must parse on a machine with no
// plugins installed, so the plugin idiom is shown only inside comments (an
// active `import "plugins:/..."` would fail to resolve with zero plugins).
const StubTemplate = `amends "formae:/Config.pkl"   // pulls in the formae config schema
// import "plugins:/Aws.pkl" as Aws   // uncomment after ` + "`formae plugin install aws`" + `

// A formae config has two halves; which matters depends on what you run here:
//   * cli   - where the CLI sends commands (apply/destroy/status/...)
//   * agent - settings for a formae agent running on THIS machine
// Running both locally (the common first-time setup) -> keep both, on localhost.
// Talking to a remote agent -> edit cli.api.url and drop the agent block.
// Only running an agent here -> keep agent, drop cli.

cli {
    api {
        url  = "http://localhost"
        port = 49684
    }
}

agent {
    server {
        hostname = "localhost"
        port     = 49684
    }

    // Once installed, register resource plugins here (and uncomment the import
    // above). See https://docs.formae.io for the full list + per-plugin options.
    //
    // resourcePlugins {
    //     new Aws.PluginConfig { }
    // }
}
`
