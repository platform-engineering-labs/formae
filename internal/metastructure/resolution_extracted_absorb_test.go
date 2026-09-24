// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package metastructure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/schema"
	jsonschema "github.com/platform-engineering-labs/formae/internal/schema/json"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestExtractedPriorAbsorbThenCanonicalProviderDefaults(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
	_, err := ds.CreateStack(&pkgmodel.Stack{Label: "service"}, "seed")
	require.NoError(t, err)
	stack, err := ds.GetStackByLabel("service")
	require.NoError(t, err)
	_, err = ds.CreateTarget(&pkgmodel.Target{Label: "test", Namespace: "Test", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)

	imageSchema := pkgmodel.Schema{
		Fields: []string{"Repository", "BuildArg", "BuildConfigHash", "ImageDigest", "ImageRef", "ImageUri"},
		Hints: map[string]pkgmodel.FieldHint{
			"BuildConfigHash": {HasProviderDefault: true},
			"ImageDigest":     {HasProviderDefault: true},
			"ImageRef":        {HasProviderDefault: true},
			"ImageUri":        {HasProviderDefault: true},
		},
		Portable: true,
	}
	initial := []pkgmodel.Resource{
		{Ksuid: "image-build", NativeID: "image-build", Managed: true, Label: "image-build", Type: "Test::ImageBuild", Stack: stack.Label, Target: "test", Schema: imageSchema, Properties: json.RawMessage(`{"Repository":"repo","BuildArg":"old","BuildConfigHash":"hash","ImageDigest":"digest","ImageRef":"repo:old","ImageUri":"registry/repo:tag"}`)},
		{Ksuid: "task-definition", NativeID: "task-definition", Managed: true, Label: "task-definition", Type: "Test::TaskDefinition", Stack: stack.Label, Target: "test", Schema: pkgmodel.Schema{Fields: []string{"Version"}, Portable: true}, Properties: json.RawMessage(`{"Version":"old"}`)},
		{Ksuid: "service", NativeID: "service", Managed: true, Label: "service", Type: "Test::Service", Stack: stack.Label, Target: "test", Schema: pkgmodel.Schema{Fields: []string{"Version"}, Portable: true}, Properties: json.RawMessage(`{"Version":"old"}`)},
	}

	fixture, err := filepath.Abs("../schema/pkl/testdata/forma/value_test.pkl")
	require.NoError(t, err)
	evaluated, err := (pkl.PKL{}).Evaluate(fixture, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, map[string]string{"name": "task3", "secret": "synthetic", "description": "synthetic"})
	require.NoError(t, err)
	var credential pkgmodel.Resource
	for _, resource := range evaluated.Resources {
		if resource.Label == "task3-stable" {
			credential = resource
			break
		}
	}
	require.NotEmpty(t, credential.Label)
	setOnce := gjson.GetBytes(credential.Properties, "SecretString")
	require.Equal(t, pkgmodel.StrategySetOnce, setOnce.Get("$strategy").String())
	require.Equal(t, pkgmodel.VisibilityOpaque, setOnce.Get("$visibility").String())
	require.NotEmpty(t, setOnce.Get("$value").String())
	credential.Ksuid = "credential"
	credential.NativeID = "credential"
	credential.Managed = true
	credential.Label = "credential"
	credential.Stack = stack.Label
	credential.Target = "test"
	initial = append(initial, credential)

	initialCommand := &forma_command.FormaCommand{
		ID: util.NewID(), StartTs: time.Now().UTC().Add(-time.Hour), ModifiedTs: time.Now().UTC().Add(-time.Hour),
		Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess,
		Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile},
		Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}},
	}
	for _, resource := range initial {
		initialCommand.ResourceUpdates = append(initialCommand.ResourceUpdates, resource_update.ResourceUpdate{DesiredState: resource, StackLabel: stack.Label, Operation: types.OperationUpdate, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess})
	}
	require.NoError(t, ds.StoreFormaCommand(initialCommand, initialCommand.ID))
	for i := range initial {
		_, err = ds.StoreResource(&initial[i], initialCommand.ID)
		require.NoError(t, err)
	}

	liveImage := initial[0]
	liveImage.Properties = json.RawMessage(`{"Repository":"repo","BuildArg":"new","BuildConfigHash":"hash","ImageDigest":"digest","ImageRef":"repo:new","ImageUri":"registry/repo:tag"}`)
	patchCommand := &forma_command.FormaCommand{
		ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply,
		Source: forma_command.SourceUser, State: forma_command.CommandStateSuccess,
		Config:          config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModePatch},
		Stacks:          []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}},
		ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: liveImage, StackLabel: stack.Label, Operation: types.OperationUpdate, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess}},
	}
	require.NoError(t, ds.StoreFormaCommand(patchCommand, patchCommand.ID))
	_, err = ds.StoreResource(&liveImage, patchCommand.ID)
	require.NoError(t, err)

	extracted, err := m.ExtractDesiredStacks("stack:service")
	require.NoError(t, err)
	require.Len(t, extracted.Resources, 4)
	priorImage := resourceByLabel(t, extracted, "image-build")
	require.Equal(t, "old", gjson.GetBytes(priorImage.Properties, "BuildArg").String())
	for _, field := range []string{"BuildConfigHash", "ImageDigest", "ImageRef", "ImageUri"} {
		require.True(t, gjson.GetBytes(priorImage.Properties, field).Exists(), field)
	}
	require.Equal(t, pkgmodel.StrategySetOnce, gjson.GetBytes(resourceByLabel(t, extracted, "credential").Properties, "SecretString.$strategy").String())
	frozenPath := writeFrozenFormaJSON(t, extracted)

	canonicalFirst := readFrozenFormaJSON(t, frozenPath)
	resourceByLabel(t, canonicalFirst, "image-build").Properties = json.RawMessage(`{"Repository":"repo","BuildArg":"old"}`)
	canonicalPath := writeFrozenFormaJSON(t, canonicalFirst)
	canonicalObservation := observeResolution(t, m, readFrozenFormaJSON(t, canonicalPath))
	_, err = m.ApplyForma(readFrozenFormaJSON(t, canonicalPath), &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: canonicalObservation.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "image-build", Action: "absorb"}}}}, "client", "subject", "")
	var conflict apimodel.DriftResolutionError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "decision-edit-conflict", conflict.Code)
	require.Contains(t, conflict.Reason, "/ImageRef")

	priorObservation := observeResolution(t, m, readFrozenFormaJSON(t, frozenPath))
	opts := &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true, Resolution: &pkgmodel.DriftResolution{ObservationID: priorObservation.ObservationID, Decisions: []pkgmodel.DriftDecision{{ResourceID: "image-build", Action: "absorb"}}}}
	changedPath := writeChangedGeneratedValueJSON(t, frozenPath)
	_, err = m.ApplyForma(readFrozenFormaJSON(t, changedPath), opts, "client", "subject", "")
	var staleObservation apimodel.DriftResolutionError
	require.ErrorAs(t, err, &staleObservation)
	require.Equal(t, "stale-review", staleObservation.Code)

	preview, err := m.ApplyForma(readFrozenFormaJSON(t, frozenPath), opts, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, preview.Simulation.Command.ResourceUpdates, 1)
	require.Equal(t, "accept", preview.Simulation.Command.ResourceUpdates[0].Operation)
	require.Empty(t, preview.Simulation.Command.ResourceUpdates[0].PatchDocument)

	opts.Simulate = false
	opts.Resolution.ReviewID = preview.Review.ReviewID
	_, err = m.prepareGuardedApply(readFrozenFormaJSON(t, changedPath), opts, "client", "subject", "")
	var staleReview apimodel.DriftResolutionError
	require.ErrorAs(t, err, &staleReview)
	require.Equal(t, "stale-review", staleReview.Code)

	accepted, err := m.prepareGuardedApply(readFrozenFormaJSON(t, frozenPath), opts, "client", "subject", "")
	require.NoError(t, err)
	require.Len(t, accepted.Command.ResourceUpdates, 1)
	require.Equal(t, resource_update.OperationAccept, accepted.Command.ResourceUpdates[0].Operation)
	require.NoError(t, admitScopedPlan(t, m, accepted))

	afterAccept, err := m.ExtractDesiredStacks("stack:service")
	require.NoError(t, err)
	acceptedImage := resourceByLabel(t, afterAccept, "image-build")
	require.Equal(t, "new", gjson.GetBytes(acceptedImage.Properties, "BuildArg").String())
	require.Equal(t, "repo:new", gjson.GetBytes(acceptedImage.Properties, "ImageRef").String())

	canonical := ownPlanningValue(afterAccept)
	resourceByLabel(t, canonical, "image-build").Properties = json.RawMessage(`{"Repository":"repo","BuildArg":"new"}`)
	resourceByLabel(t, canonical, "task-definition").Properties = json.RawMessage(`{"Version":"new"}`)
	resourceByLabel(t, canonical, "service").Properties = json.RawMessage(`{"Version":"new"}`)
	result, err := m.ApplyForma(canonical, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "subject", "")
	require.NoError(t, err)
	var updated []string
	for _, update := range result.Simulation.Command.ResourceUpdates {
		updated = append(updated, update.ResourceLabel)
	}
	sort.Strings(updated)
	require.Equal(t, []string{"service", "task-definition"}, updated)
}

func writeFrozenFormaJSON(t *testing.T, forma *pkgmodel.Forma) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "desired.json")
	_, err := (jsonschema.JSON{}).GenerateSourceCode(forma, path, nil, &schema.SerializeOptions{Schema: "json"})
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	return path
}

func readFrozenFormaJSON(t *testing.T, path string) *pkgmodel.Forma {
	t.Helper()
	forma, err := (jsonschema.JSON{}).Evaluate(path, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	return forma
}

func writeChangedGeneratedValueJSON(t *testing.T, sourcePath string) string {
	t.Helper()
	changed := readFrozenFormaJSON(t, sourcePath)
	credential := resourceByLabel(t, changed, "credential")
	current := gjson.GetBytes(credential.Properties, "SecretString.$value").String()
	require.NotEmpty(t, current)
	properties, err := sjson.SetBytes(credential.Properties, "SecretString.$value", current+"-changed")
	require.NoError(t, err)
	credential.Properties = properties
	return writeFrozenFormaJSON(t, changed)
}

func resourceByLabel(t *testing.T, forma *pkgmodel.Forma, label string) *pkgmodel.Resource {
	t.Helper()
	for i := range forma.Resources {
		if forma.Resources[i].Label == label {
			return &forma.Resources[i]
		}
	}
	t.Fatalf("resource %q not found", label)
	return nil
}
