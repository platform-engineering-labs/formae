// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// CommandsMsgForTest constructs the internal commandsMsg tea.Msg so external
// test packages can inject commands into a Model without importing unexported types.
func CommandsMsgForTest(cmds []apimodel.Command) interface{} {
	return commandsMsg{commands: cmds}
}
