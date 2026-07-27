// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package destroy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDestroyCmd_NoWatchFlag guards the removal of --watch: destroy watches by
// default on a TTY and is fire-and-forget otherwise, so the flag no longer
// exists.
func TestDestroyCmd_NoWatchFlag(t *testing.T) {
	c := DestroyCmd()
	assert.Nil(t, c.Flags().Lookup("watch"), "--watch must not exist on destroy")
	assert.NotNil(t, c.Flags().Lookup("simulate"), "simulate flag should still exist")
}
