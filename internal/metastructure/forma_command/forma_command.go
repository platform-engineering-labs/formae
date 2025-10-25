// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package forma_command

import (
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type CommandState string

const (
	CommandStateUnknown    CommandState = "Unknown"
	CommandStateNotStarted CommandState = "NotStarted"
	CommandStatePending    CommandState = "Pending"
	CommandStateInProgress CommandState = "InProgress"
	CommandStateFailed     CommandState = "Failed"
	CommandStateSuccess    CommandState = "Success"
	CommandStateCanceled   CommandState = "Canceled"
)

type FormaCommand struct {
	ID              string                           `json:"ID"`
	Forma           pkgmodel.Forma                   `json:"Forma"`
	State           CommandState                     `json:"State"`
	StartTs         time.Time                        `json:"StartTs"`
	ModifiedTs      time.Time                        `json:"ModifiedTs"`
	ResourceUpdates []resource_update.ResourceUpdate `json:"ResourceUpdates,omitempty"`
	Config          config.FormaCommandConfig        `json:"Config"`
	Command         pkgmodel.Command                 `json:"Command"`
	ClientID        string                           `json:"ClientId,omitempty"`
}

type FormaCommandResult struct {
	Command *FormaCommand `json:"Command"`
	State   CommandState  `json:"State"`
}

type FormaCommandsStatusResult struct {
	Commands []FormaCommandResult `json:"Commands"`
}

func NewFormaCommand(
	forma *pkgmodel.Forma,
	formaCommandConfig *config.FormaCommandConfig,
	command pkgmodel.Command,
	resourceUpdates []resource_update.ResourceUpdate,
	clientID string,
) *FormaCommand {
	return &FormaCommand{
		ID:              util.NewID(),
		StartTs:         util.TimeNow(),
		ModifiedTs:      util.TimeNow(),
		ResourceUpdates: resourceUpdates,
		Config:          *formaCommandConfig,
		Command:         command,
		Forma:           *forma,
		State:           CommandStateNotStarted,
		ClientID:        clientID,
	}
}
