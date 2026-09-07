// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package cancel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCancelCmd_NoWatchFlag guards the removal of --watch (-w): cancel watches
// by default on a TTY and is fire-and-forget otherwise, so the flag no longer
// exists.
func TestCancelCmd_NoWatchFlag(t *testing.T) {
	c := CancelCmd()
	assert.Nil(t, c.Flags().Lookup("watch"), "--watch must not exist on cancel")
	assert.Nil(t, c.Flags().ShorthandLookup("w"), "-w shorthand must not exist on cancel")
	assert.NotNil(t, c.Flags().Lookup("force"), "force flag should still exist")
}
