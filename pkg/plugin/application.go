// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package plugin

import (
	"ergo.services/ergo/gen"
)

// PluginApplication is the Ergo application that manages plugin actors.
type PluginApplication struct{}

func createPluginApplication() gen.ApplicationBehavior {
	return &PluginApplication{}
}

func (app *PluginApplication) Load(node gen.Node, args ...any) (gen.ApplicationSpec, error) {
	return gen.ApplicationSpec{
		Name:        "PluginApplication",
		Description: "Plugin lifecycle management",
		Mode:        gen.ApplicationModePermanent,
		Group: []gen.ApplicationMemberSpec{
			{
				Name:    "PluginActor",
				Factory: factoryPluginActor,
			},
		},
	}, nil
}

func (app *PluginApplication) Start(mode gen.ApplicationMode) {}

func (app *PluginApplication) Terminate(reason error) {}
