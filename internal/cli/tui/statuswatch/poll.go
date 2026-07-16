// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package statuswatch

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// Client is the API surface the status/watch TUI needs. *app.App satisfies it.
type Client interface {
	GetCommandsStatus(query string, n int, fromWatch bool) (*apimodel.ListCommandStatusResponse, []string, error)
}

type commandsMsg struct {
	commands []apimodel.Command
	err      error
}

type tickMsg struct{}

func fetchCommands(c Client, query string, max int) tea.Cmd {
	return func() tea.Msg {
		resp, _, err := c.GetCommandsStatus(query, max, true)
		if err != nil {
			return commandsMsg{err: err}
		}
		if resp == nil {
			return commandsMsg{}
		}
		return commandsMsg{commands: resp.Commands}
	}
}

func tick(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg { return tickMsg{} })
}
