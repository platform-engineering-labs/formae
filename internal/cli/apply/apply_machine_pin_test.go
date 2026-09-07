// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package apply

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/cli/printer"
	"github.com/platform-engineering-labs/formae/internal/cli/tui/tuitest"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
)

// machineSimFixture is a fixed Simulation used to pin the machine output bytes.
// Any change to the machine branch output will break this test.
var machineSimFixture = apimodel.Simulation{
	ChangesRequired: true,
	Command: apimodel.Command{
		CommandID: "pin-cmd-001",
		Command:   "apply",
		Mode:      "reconcile",
		ResourceUpdates: []apimodel.ResourceUpdate{
			{ResourceID: "r1", ResourceLabel: "my-bucket", ResourceType: "AWS::S3::Bucket", StackName: "default", Operation: "create"},
			{ResourceID: "r2", ResourceLabel: "old-queue", ResourceType: "AWS::SQS::Queue", StackName: "default", Operation: "delete"},
		},
	},
}

func TestMachinePinApplySimulate_JSON(t *testing.T) {
	var buf bytes.Buffer
	p := printer.NewMachineReadablePrinter[apimodel.Simulation](&buf, "json")
	err := p.Print(&machineSimFixture)
	require.NoError(t, err)

	got := buf.Bytes()

	// Verify it round-trips as valid JSON
	var roundtrip apimodel.Simulation
	require.NoError(t, json.Unmarshal(got, &roundtrip), "machine output must be valid JSON")

	// Pin exact bytes against a frozen golden file (not recomputed from fixture).
	tuitest.RequireGolden(t, got)
}
