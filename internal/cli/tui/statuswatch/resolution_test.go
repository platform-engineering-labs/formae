//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package statuswatch

import (
	"github.com/platform-engineering-labs/formae/internal/cli/tui/theme"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestStatusDistinguishesAcceptanceFromProviderWork(t *testing.T) {
	command := apimodel.Command{CommandID: "recorded", State: "Success", ResourceUpdates: []apimodel.ResourceUpdate{{Operation: "accept", State: "Success", ResourceLabel: "kept"}, {Operation: "accept_delete", State: "Success", ResourceLabel: "gone"}, {Operation: "update", State: "Success", ResourceLabel: "changed"}}}
	text := RenderDetailTable(theme.New("formae"), command, 140, time.Now())
	require.Contains(t, text, "Drift acceptance records: 2; provider resource operations: 1")
}
