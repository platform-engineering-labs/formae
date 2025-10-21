// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"log/slog"
	"time"

	"ergo.services/ergo/gen"
	"ergo.services/ergo/net/edf"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/messages"
	internalTypes "github.com/platform-engineering-labs/formae/internal/metastructure/types"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	pkgresource "github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func initDelegateMessageTypes() error {
	types := []any{
		forma_command.CommandStatePending,
		time.Duration(0),
		pkgmodel.FormaApplyModeReconcile,

		pkgresource.OperationStatus(""),
		pkgresource.Operation(""),
		pkgresource.OperationErrorCode(""),
		pkgresource.ProgressResult{},

		pkgmodel.FormaeURI(""),
		pkgmodel.FieldHint{},
		pkgmodel.Schema{},
		pkgmodel.Resource{},
		pkgmodel.Stack{},
		pkgmodel.Target{},
		pkgmodel.Prop{},
		pkgmodel.Description{},
		pkgmodel.Forma{},

		internalTypes.OperationType(""),
		internalTypes.ResourceUpdateState(""),
		internalTypes.FormaCommandSource(""),

		config.FormaCommandConfig{},

		messages.MarkResourceUpdateAsComplete{},
		messages.UpdateResourceProgress{},

		pkgmodel.Command(""),
	}

	for _, t := range types {
		err := edf.RegisterTypeOf(t)
		if err == nil || err == gen.ErrTaken {
			continue
		}

		slog.Error("failed to register type", "type", t, "error", err)
		return err
	}

	return nil
}
