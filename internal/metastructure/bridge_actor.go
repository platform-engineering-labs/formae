// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"fmt"

	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/internal/metastructure/changeset"
)

// MetastructureBridge is an actor that bridges the non-actor world (metastructure API)
// with the actor world by providing synchronous call semantics to non-actor code.
// The desire is to make the metastructure in and of itself in the future.
type MetastructureBridge struct {
	act.Actor
}

func NewMetastructureBridge() gen.ProcessBehavior {
	return &MetastructureBridge{}
}

// CallActorRequest is a message sent to the bridge to make a synchronous call to another actor.
type CallActorRequest struct {
	TargetPID   gen.ProcessID
	Message     any
	SuccessChan chan any
	ErrorChan   chan error
}

func (b *MetastructureBridge) Init(args ...any) error {
	b.Log().Debug("MetastructureBridge initialized")
	return nil
}

func (b *MetastructureBridge) HandleMessage(from gen.PID, message any) error {
	switch msg := message.(type) {
	case CallActorRequest:
		// Make the synchronous call to the target actor
		response, err := b.CallWithTimeout(msg.TargetPID, msg.Message, 10)

		// Send the result back through the appropriate channel
		if err != nil {
			select {
			case msg.ErrorChan <- err:
			default:
				b.Log().Error("failed to send error response to channel", "error", err)
			}
		} else {
			select {
			case msg.SuccessChan <- response:
			default:
				b.Log().Error("failed to send success response to channel")
			}
		}

		return nil

	case changeset.ChangesetCompleted:
		// Child actors (like ChangesetExecutor) send completion messages to their parent.
		// Since the bridge acts as a parent when ensuring child actors, we receive these
		// completion messages. The metastructure uses a fire-and-forget pattern and doesn't
		// need these notifications, so we simply ignore them.
		b.Log().Debug("Ignoring changeset completion message (fire-and-forget pattern)", "commandID", msg.CommandID)
		return nil

	default:
		return fmt.Errorf("MetastructureBridge received unknown message type %T", message)
	}
}
