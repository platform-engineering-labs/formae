// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package store

// StubTemplate is the minimal PKL config written by `formae profile create` and
// by clean-install bootstrap. It materializes NO config values: it only `amends`
// the schema, which already wires both halves of a formae config to localhost.
// Keeping it value-free means the file inherits schema evolution automatically
// (no frozen snapshot to drift), and the worked overrides live only in comments —
// comments never resolve imports, so the stub also parses on a machine with zero
// plugins installed (an active `import "plugins:/..."` would not).
const StubTemplate = `amends "formae:/Config.pkl"   // the formae config schema — defaults already wire localhost
// import "plugins:/Aws.pkl" as Aws   // uncomment after ` + "`formae plugin install aws`" + `

// This file inherits everything from the schema, which is a complete working
// LOCAL setup out of the box. A formae config has two halves:
//   * cli   - where the CLI sends commands (apply/destroy/status/...)  [default http://localhost:49684]
//   * agent - settings for a formae agent running on THIS machine      [default localhost:49684]
//
// Override only what you need, for example:
//
//   // Point the CLI at a remote agent:
//   cli {
//       api {
//           url  = "http://my-agent.example.com"
//           port = 49684
//       }
//   }
//
//   // Register a resource plugin (after ` + "`formae plugin install aws`" + ` + uncomment the import above):
//   agent {
//       resourcePlugins {
//           new Aws.PluginConfig { }
//       }
//   }
//
// See https://docs.formae.io for the full list of options.
`
