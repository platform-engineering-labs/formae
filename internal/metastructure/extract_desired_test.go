//go:build unit

// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2
package metastructure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/platform-engineering-labs/formae/internal/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/types"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/schema"
	"github.com/platform-engineering-labs/formae/internal/schema/pkl"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/require"
)

func desiredExtractor(t *testing.T, m *Metastructure) func(string) (*pkgmodel.Forma, error) {
	t.Helper()
	e, ok := any(m).(interface {
		ExtractDesiredStacks(string) (*pkgmodel.Forma, error)
	})
	require.True(t, ok, "complete desired extraction must be available separately from actual extraction")
	return e.ExtractDesiredStacks
}
func storeDesired(t *testing.T, ds datastore.Datastore, r pkgmodel.Resource, op types.OperationType, state forma_command.CommandState) {
	t.Helper()
	stack, err := ds.GetStackByLabel(r.Stack)
	require.NoError(t, err)
	require.NotNil(t, stack)
	cmd := &forma_command.FormaCommand{ID: util.NewID(), StartTs: time.Now().UTC(), ModifiedTs: time.Now().UTC(), Command: pkgmodel.CommandApply, Source: forma_command.SourceUser, State: state, Config: config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile}, Stacks: []forma_command.CommandStack{{ID: stack.ID, Label: stack.Label}}, ResourceUpdates: []resource_update.ResourceUpdate{{DesiredState: r, StackLabel: r.Stack, Operation: op, Source: resource_update.FormaCommandSourceUser, State: resource_update.ResourceUpdateStateSuccess}}}
	require.NoError(t, ds.StoreFormaCommand(cmd, cmd.ID))
}
func TestExtractDesiredStacks_CompleteIntent(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
	extract := desiredExtractor(t, m)
	for _, label := range []string{"main", "empty"} {
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "seed")
		require.NoError(t, err)
	}
	_, err := ds.CreateTarget(&pkgmodel.Target{Label: "target", Namespace: "Test", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	r := pkgmodel.Resource{Ksuid: util.NewID(), Label: "resource", Type: "Test::Resource", Stack: "main", Target: "target", Managed: true, Properties: json.RawMessage(`{"name":"desired","tags":{"app":"mine","platform":"theirs"}}`), Schema: pkgmodel.Schema{Fields: []string{"name", "tags"}, Hints: map[string]pkgmodel.FieldHint{"tags": {CoOwned: &pkgmodel.CoOwnership{}}}}, OwnedMembers: pkgmodel.OwnedMembers{"tags": {Rule: "Mapping", Members: []string{"app"}}}}
	storeDesired(t, ds, r, types.OperationAccept, forma_command.CommandStateFailed)
	live := r
	live.Properties = json.RawMessage(`{"name":"unsettled-patch","tags":{"app":"mine","platform":"theirs"}}`)
	_, err = ds.StoreResource(&live, "patch")
	require.NoError(t, err)
	extra := live
	extra.Ksuid = util.NewID()
	extra.Label = "patch-only"
	_, err = ds.StoreResource(&extra, "patch")
	require.NoError(t, err)
	got, err := extract("stack:main stack:empty")
	require.NoError(t, err)
	require.Len(t, got.Stacks, 2)
	require.Len(t, got.Resources, 1)
	require.JSONEq(t, `{"name":"desired","tags":{"app":"mine"}}`, string(got.Resources[0].Properties))
	require.Empty(t, got.Resources[0].OwnedMembers)
	require.Len(t, got.Targets, 1)
	for _, query := range []string{"", "type:Test", "stack:main label:resource", "stack:$unmanaged", "stack:missing"} {
		_, err = extract(query)
		require.Error(t, err, query)
	}
	storeDesired(t, ds, r, types.OperationAcceptDelete, forma_command.CommandStateCanceled)
	got, err = extract("stack:main")
	require.NoError(t, err)
	require.Len(t, got.Resources, 1)
	storeDesired(t, ds, r, types.OperationAcceptDelete, forma_command.CommandStateSuccess)
	got, err = extract("stack:main")
	require.NoError(t, err)
	require.Len(t, got.Stacks, 1)
	require.Empty(t, got.Resources, "patch-only inventory is not desired state")
}

func TestExtractDesiredStacks_PklRoundTrip(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
	extract := desiredExtractor(t, m)
	for _, label := range []string{"ttl-stack", "plain-stack", "external-owner"} {
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "seed")
		require.NoError(t, err)
	}
	stack, err := ds.GetStackByLabel("ttl-stack")
	require.NoError(t, err)
	_, err = ds.CreatePolicy(&pkgmodel.TTLPolicy{Type: "ttl", TTLSeconds: 3600, OnDependents: "abort", Label: "deadline", StackID: stack.ID}, "policy")
	require.NoError(t, err)
	_, err = ds.CreatePolicy(&pkgmodel.AutoReconcilePolicy{Type: "auto-reconcile", IntervalSeconds: 120, Label: "periodic"}, "policy")
	require.NoError(t, err)
	require.NoError(t, ds.AttachPolicyToStack(stack.ID, "periodic"))
	for _, label := range []string{"ttl-stack", "plain-stack", "external-owner"} {
		owner, e := ds.GetStackByLabel(label)
		require.NoError(t, e)
		_, err = ds.CreateGenerator(&pkgmodel.PasswordGenerator{Label: "credential", Stack: label, StackID: owner.ID, Length: 24, Uppercase: true, Lowercase: true, Digits: true, Symbols: true, RequireEachIncludedType: true}, "generator")
		require.NoError(t, err)
	}
	forma, err := extract("stack:ttl-stack stack:plain-stack")
	require.NoError(t, err)
	emptyRound := desiredPklRoundTrip(t, forma)
	metadataPlan, e := FormaCommandFromForma(emptyRound, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, pkgmodel.CommandApply, ds, "client", "", "", resource_update.FormaCommandSourceUser, 0)
	require.NoError(t, e)
	require.Empty(t, metadataPlan.PolicyUpdates)
	require.Empty(t, metadataPlan.GeneratorUpdates)
	require.Empty(t, metadataPlan.ResourceUpdates)
	// A selected resource uses an external generator but never declares its owner.
	forma.Targets = []pkgmodel.Target{{Label: "aws", Namespace: "FakeAWS", Config: json.RawMessage(`{"Type":"FakeAWS","Region":"us-east-1"}`), Reaping: json.RawMessage(`{"Kind":"never"}`)}}
	forma.Resources = []pkgmodel.Resource{{Label: "secret", Stack: "plain-stack", Target: "aws", Type: "FakeAWS::SecretsManager::Secret", Properties: json.RawMessage(`{"SecretString":{"$gen":true,"$label":"credential","$stack":"external-owner","$output":"value","$visibility":"Opaque"}}`)}}
	// Store as accepted desired and reread the actual extraction path.
	_, err = ds.CreateTarget(&forma.Targets[0])
	require.NoError(t, err)
	forma.Resources[0].Ksuid = util.NewID()
	forma.Resources[0].Group = "deployment-group"
	forma.Resources[0].Alias = "previous-secret"
	storeDesired(t, ds, forma.Resources[0], types.OperationAccept, forma_command.CommandStateSuccess)
	forma, err = extract("stack:ttl-stack stack:plain-stack")
	require.NoError(t, err)
	require.Len(t, forma.Extraction.ReferenceGenerators, 1)
	evaluated := desiredPklRoundTrip(t, forma)
	require.Len(t, evaluated.Stacks, 2)
	require.Len(t, evaluated.Generators, 2)
	require.Len(t, evaluated.Resources, 1)
	require.Equal(t, forma.Resources[0].Group, evaluated.Resources[0].Group)
	require.Equal(t, forma.Resources[0].Alias, evaluated.Resources[0].Alias)
	require.JSONEq(t, string(forma.Resources[0].Properties), string(evaluated.Resources[0].Properties))
	require.JSONEq(t, string(forma.Targets[0].Reaping), string(evaluated.Targets[0].Reaping))
	require.Len(t, evaluated.Policies, 1)
	policies := map[string]int{}
	for _, s := range evaluated.Stacks {
		policies[s.Label] = len(s.Policies)
	}
	require.Equal(t, map[string]int{"ttl-stack": 2, "plain-stack": 0}, policies)
}

func desiredPklRoundTrip(t *testing.T, forma *pkgmodel.Forma) *pkgmodel.Forma {
	return desiredPklRoundTripWithFixture(t, forma, false)
}
func desiredPklRoundTripWithFixture(t *testing.T, forma *pkgmodel.Forma, extended bool) *pkgmodel.Forma {
	t.Helper()
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	options := &schema.SerializeOptions{Schema: "pkl", SchemaLocation: schema.SchemaLocationLocal, Dependencies: []string{"local:formae:" + filepath.Join(root, "internal/schema/pkl/schema/PklProject"), "local:fakeaws:" + filepath.Join(root, "internal/testplugin/fakeaws/schema/pkl/PklProject")}}
	if extended {
		fixture := filepath.Join(t.TempDir(), "fakeaws")
		require.NoError(t, os.CopyFS(fixture, os.DirFS(filepath.Join(root, "internal/testplugin/fakeaws/schema/pkl"))))
		project := filepath.Join(fixture, "PklProject")
		raw, err := os.ReadFile(project)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(project, []byte(strings.ReplaceAll(string(raw), "../../../../schema/pkl/schema/PklProject", filepath.Join(root, "internal/schema/pkl/schema/PklProject"))), 0600))
		secret := filepath.Join(fixture, "secretsmanager/secret.pkl")
		raw, err = os.ReadFile(secret)
		require.NoError(t, err)
		content := strings.ReplaceAll(string(raw), "(String|formae.ValueSource)?", "(String|formae.ValueSource|formae.Embedded)?")
		content = strings.ReplaceAll(content, "@fakeaws.FieldHint\n    tags: Listing<fakeaws.Tag>?", "@fakeaws.FieldHint { coOwned = new formae.CoOwnership {} }\n    tags: Mapping<String, String>?")
		require.NoError(t, os.WriteFile(secret, []byte(content), 0600))
		options.Dependencies[1] = "local:fakeaws:" + project
	}
	path := filepath.Join(t.TempDir(), "desired.pkl")
	_, err = pkl.PKL{}.GenerateSourceCode(forma, path, nil, options)
	require.NoError(t, err)
	evaluated, err := pkl.PKL{}.Evaluate(path, pkgmodel.CommandApply, pkgmodel.FormaApplyModeReconcile, nil)
	require.NoError(t, err)
	return evaluated
}

func TestDesiredOwnershipAuthoritativeForPlanning(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
	_, err := ds.CreateStack(&pkgmodel.Stack{Label: "owned"}, "seed")
	require.NoError(t, err)
	target := pkgmodel.Target{Label: "t", Namespace: "FakeAWS", Config: json.RawMessage(`{"Type":"FakeAWS","Region":"us-east-1"}`)}
	_, err = ds.CreateTarget(&target)
	require.NoError(t, err)
	r := pkgmodel.Resource{Ksuid: util.NewID(), NativeID: "native", Label: "resource", Stack: "owned", Target: "t", Type: "FakeAWS::SecretsManager::Secret", Managed: true, Properties: json.RawMessage(`{"Tags":{"app":"one","adopted":"two"}}`), Schema: pkgmodel.Schema{Fields: []string{"Tags"}, Hints: map[string]pkgmodel.FieldHint{"Tags": {CoOwned: &pkgmodel.CoOwnership{}}}}, OwnedMembers: pkgmodel.OwnedMembers{"Tags": {Rule: "Mapping", Members: []string{"app"}}}}
	_, err = ds.StoreResource(&r, "seed")
	require.NoError(t, err)
	before, err := ds.LoadResourceById(r.Ksuid)
	require.NoError(t, err)
	accepted := r
	accepted.OwnedMembers = pkgmodel.OwnedMembers{"Tags": {Rule: "Mapping", Members: []string{"app", "adopted"}}}
	storeDesired(t, ds, accepted, types.OperationAccept, forma_command.CommandStateSuccess)
	forma, err := desiredExtractor(t, m)("stack:owned")
	require.NoError(t, err)
	forma = desiredPklRoundTripWithFixture(t, forma, true)
	command, err := FormaCommandFromForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, pkgmodel.CommandApply, ds, "client", "", "", resource_update.FormaCommandSourceUser, 0)
	require.NoError(t, err)
	require.Empty(t, command.ResourceUpdates, "accepted ownership is already desired: no metadata-only inventory update")
	unrelated := forma.Resources[0]
	unrelated.Label = "unrelated"
	unrelated.Ksuid = ""
	unrelated.Properties = json.RawMessage(`{"Name":"unrelated"}`)
	forma.Resources = append(forma.Resources, unrelated)
	command, err = FormaCommandFromForma(forma, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, pkgmodel.CommandApply, ds, "client", "", "", resource_update.FormaCommandSourceUser, 0)
	require.NoError(t, err)
	require.Len(t, command.ResourceUpdates, 1)
	require.Equal(t, "unrelated", command.ResourceUpdates[0].DesiredState.Label)
	after, err := ds.LoadResourceById(r.Ksuid)
	require.NoError(t, err)
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, r.OwnedMembers, after.OwnedMembers)
	snapshots, err := ds.GetResourcesAtLastReconcile(r.Stack)
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	automatic := reconcileResourceFromSnapshot(snapshots[0], after, r.Stack)
	require.Equal(t, accepted.OwnedMembers, automatic.OwnedMembers)
}

func TestExtractDesiredStacks_GeneratorIdentityAfterRenameAndLabelReuse(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	_, err := ds.CreateStack(&pkgmodel.Stack{Label: "stack"}, "seed")
	require.NoError(t, err)
	stack, err := ds.GetStackByLabel("stack")
	require.NoError(t, err)
	g := &pkgmodel.PasswordGenerator{Label: "old", Stack: "stack", StackID: stack.ID, Length: 24, Uppercase: true, Lowercase: true, Digits: true, Symbols: true, RequireEachIncludedType: true}
	_, err = ds.CreateGenerator(g, "seed")
	require.NoError(t, err)
	identity, err := ds.GetGeneratorIdentity("old", "stack")
	require.NoError(t, err)
	spec, err := json.Marshal(g)
	require.NoError(t, err)
	require.NoError(t, ds.AdvanceGeneration(identity.ID, util.NewID(), "draw", spec))
	g.Label = "renamed"
	g.Alias = "old"
	_, err = ds.UpdateGenerator(g, "rename")
	require.NoError(t, err)
	replacement := *g
	replacement.Label = "old"
	replacement.Alias = ""
	replacement.ID = ""
	_, err = ds.CreateGenerator(&replacement, "replacement")
	require.NoError(t, err)
	_, err = ds.CreateTarget(&pkgmodel.Target{Label: "target", Namespace: "Test", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	props, err := json.Marshal(map[string]any{"secret": map[string]any{"$gen": true, "$generator": identity.ID, "$output": "value", "$visibility": "Opaque"}})
	require.NoError(t, err)
	r := pkgmodel.Resource{Ksuid: util.NewID(), Label: "resource", Stack: "stack", Type: "Test::Resource", Target: "target", Properties: props}
	storeDesired(t, ds, r, types.OperationAccept, forma_command.CommandStateSuccess)
	f, err := desiredExtractor(t, &Metastructure{Datastore: ds})("stack:stack")
	require.NoError(t, err)
	require.Contains(t, string(f.Resources[0].Properties), `"$label":"renamed"`, "old label now belongs to a different generator")
}

func TestExtractDesiredStacks_ExternalResourceReferenceRoundTrip(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	for _, label := range []string{"owner", "consumer"} {
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "seed")
		require.NoError(t, err)
	}
	target := pkgmodel.Target{Label: "aws", Namespace: "FakeAWS", Config: json.RawMessage(`{"Type":"FakeAWS","Region":"us-east-1"}`)}
	_, err := ds.CreateTarget(&target)
	require.NoError(t, err)
	source := pkgmodel.Resource{Ksuid: util.NewID(), Label: "producer", Stack: "owner", Target: "aws", Type: "FakeAWS::SecretsManager::Secret", Properties: json.RawMessage(`{"Name":"producer"}`)}
	_, err = ds.StoreResource(&source, "seed")
	require.NoError(t, err)
	storeDesired(t, ds, source, types.OperationCreate, forma_command.CommandStateSuccess)
	ref := map[string]any{"$ref": "formae://" + source.Ksuid + "#/Arn", "$visibility": "Opaque"}
	embedded, err := json.Marshal(ref)
	require.NoError(t, err)
	properties, err := json.Marshal(map[string]any{"Name": "consumer", "SecretString": map[string]any{"$embed": true, "$template": "prefix-" + pkgmodel.FrameEnvelope(string(embedded))}})
	require.NoError(t, err)
	consumer := pkgmodel.Resource{Ksuid: util.NewID(), Label: "consumer", Stack: "consumer", Target: "aws", Type: source.Type, Properties: properties}
	storeDesired(t, ds, consumer, types.OperationCreate, forma_command.CommandStateSuccess)
	direct := consumer
	direct.Ksuid = util.NewID()
	direct.Label = "direct"
	direct.Properties, err = json.Marshal(map[string]any{"Name": "direct", "SecretString": ref})
	require.NoError(t, err)
	storeDesired(t, ds, direct, types.OperationCreate, forma_command.CommandStateSuccess)
	f, err := (&Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}).ExtractDesiredStacks("stack:consumer")
	require.NoError(t, err)
	round := desiredPklRoundTripWithFixture(t, f, true)
	require.Len(t, round.Stacks, 1)
	require.Equal(t, "consumer", round.Stacks[0].Label)
	for i := range f.Resources {
		if f.Resources[i].Label == "consumer" {
			var before, after map[string]any
			require.NoError(t, json.Unmarshal(f.Resources[i].Properties, &before))
			require.NoError(t, json.Unmarshal(round.Resources[i].Properties, &after))
			b := before["SecretString"].(map[string]any)["$template"].(string)
			a := after["SecretString"].(map[string]any)["$template"].(string)
			bs, err := pkgmodel.ScanEmbedSpans(b)
			require.NoError(t, err)
			as, err := pkgmodel.ScanEmbedSpans(a)
			require.NoError(t, err)
			require.Len(t, bs, 1)
			require.Len(t, as, 1)
			require.Equal(t, b[:bs[0].Start], a[:as[0].Start])
			require.JSONEq(t, bs[0].EnvelopeJSON, as[0].EnvelopeJSON)
		} else {
			require.JSONEq(t, string(f.Resources[i].Properties), string(round.Resources[i].Properties))
		}
	}
}

type desiredReadBarrier struct{ *scopedReadBarrier }

func (d *desiredReadBarrier) GetResourcesAtLastReconcile(label string) ([]datastore.ResourceSnapshot, error) {
	rows, err := d.Datastore.GetResourcesAtLastReconcile(label)
	if err == nil && d.afterRead != nil {
		f := d.afterRead
		d.afterRead = nil
		f()
	}
	return rows, err
}
func TestExtractDesiredStacksRejectsConcurrentDeclarationWrite(t *testing.T) {
	m, writer, _, _ := scopedFixture(t)
	m.Datastore = &desiredReadBarrier{withScopedBarrier(m.Datastore, func() {
		stack, err := writer.GetStackByLabel("a")
		require.NoError(t, err)
		stack.Description = "concurrent"
		_, err = writer.UpdateStack(stack, "independent-writer")
		require.NoError(t, err)
	})}
	f, err := m.ExtractDesiredStacks("stack:a")
	require.ErrorIs(t, err, datastore.ErrStaleAdmission)
	require.Nil(t, f)
	commands, err := writer.LoadFormaCommands()
	require.NoError(t, err)
	require.Empty(t, commands)
}
func TestDesiredStackSelectionUsesExistingQuotedGrammar(t *testing.T) {
	labels, err := desiredStackSelection(`stack:"space label" stack:ordinary`)
	require.NoError(t, err)
	require.Equal(t, []string{"ordinary", "space label"}, labels)
	for _, q := range []string{`stack:*`, `-stack:a`, `stack:a type:T`, `stack:a OR stack:b`, `stack:a AND stack:b`} {
		_, err = desiredStackSelection(q)
		require.Error(t, err, q)
	}
}

func TestExtractDesiredStacks_TargetConfigurationRoundTrip(t *testing.T) {
	ds := newSQLiteTestDatastore(t)
	for _, label := range []string{"owner", "consumer"} {
		_, err := ds.CreateStack(&pkgmodel.Stack{Label: label}, "seed")
		require.NoError(t, err)
	}
	owner, err := ds.GetStackByLabel("owner")
	require.NoError(t, err)
	_, err = ds.CreateGenerator(&pkgmodel.PasswordGenerator{Label: "credential", Stack: "owner", StackID: owner.ID, Length: 24, Uppercase: true, Lowercase: true, Digits: true, Symbols: true, RequireEachIncludedType: true}, "seed")
	require.NoError(t, err)
	source := pkgmodel.Resource{Ksuid: util.NewID(), Label: "source", Stack: "owner", Target: "source-target", Type: "FakeAWS::SecretsManager::Secret", Managed: true, Properties: json.RawMessage(`{"Name":"source","SecretString":{"$value":"opaque-digest","$hashed":true,"$visibility":"Opaque","$strategy":"Update"}}`)}
	_, err = ds.StoreResource(&source, "seed")
	require.NoError(t, err)
	storeDesired(t, ds, source, types.OperationCreate, forma_command.CommandStateSuccess)
	cfg, err := json.Marshal(map[string]any{
		"Type": "FakeAWS", "Region": "us-east-1", "profile": "line1\nline2\\literal\"quote",
		"Structured": map[string]any{"hyphen-key": []any{true, nil, "\\exact", json.Number("9007199254740993")}, "__literal": "retained"},
		"Endpoint":   map[string]any{"$res": true, "$label": source.Label, "$stack": source.Stack, "$type": source.Type, "$property": "SecretString", "$visibility": "Opaque"},
		"Password":   map[string]any{"$gen": true, "$label": "credential", "$stack": "owner", "$output": "value", "$visibility": "Opaque"},
	})
	require.NoError(t, err)
	target := pkgmodel.Target{Label: "aws", Namespace: "FakeAWS", Config: cfg, ConfigSchema: pkgmodel.ConfigSchema{Hints: map[string]pkgmodel.ConfigFieldHint{"Region": {CreateOnly: true}, "Profile": {CreateOnly: false}, "Omitted": {CreateOnly: true}}}}
	seed := &pkgmodel.Forma{Targets: []pkgmodel.Target{target}}
	_, _, err = resource_update.TranslateFormaeReferencesToKsuid(seed, ds)
	require.NoError(t, err)
	target = seed.Targets[0]
	var storedConfig map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(target.Config)))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&storedConfig))
	storedConfig["Endpoint"].(map[string]any)["$visibility"] = "Opaque"
	storedConfig["Structured"].(map[string]any)["hyphen-key"].([]any)[3] = json.Number("9007199254740993")
	target.Config, err = json.Marshal(storedConfig)
	require.NoError(t, err)
	_, err = ds.CreateTarget(&target)
	require.NoError(t, err)
	resource := pkgmodel.Resource{Ksuid: util.NewID(), Label: "consumer", Stack: "consumer", Target: "aws", Type: source.Type, Managed: true, Properties: json.RawMessage(`{"Name":"consumer"}`)}
	_, err = ds.StoreResource(&resource, "seed")
	require.NoError(t, err)
	storeDesired(t, ds, resource, types.OperationCreate, forma_command.CommandStateSuccess)
	m := &Metastructure{Datastore: ds, Cfg: &pkgmodel.Config{}}
	f, err := m.ExtractDesiredStacks("stack:consumer")
	require.NoError(t, err)
	round := desiredPklRoundTrip(t, f)
	require.Len(t, round.Stacks, 1)
	require.Empty(t, round.Generators)
	require.Equal(t, target.ConfigSchema, round.Targets[0].ConfigSchema)
	require.JSONEq(t, string(f.Targets[0].Config), string(round.Targets[0].Config))
	require.Contains(t, string(f.Targets[0].Config), "9007199254740993")
	require.Contains(t, string(round.Targets[0].Config), "9007199254740993")
	result, err := m.ApplyForma(round, &config.FormaCommandConfig{Mode: pkgmodel.FormaApplyModeReconcile, Simulate: true}, "client", "", "")
	require.NoError(t, err)
	require.False(t, result.Simulation.ChangesRequired)
	require.Empty(t, result.Simulation.Command.TargetUpdates)
	require.Empty(t, result.Simulation.Command.ResourceUpdates)
}

func (d *desiredReadBarrier) GetDesiredInlinePoliciesForStack(id string) ([]pkgmodel.Policy, error) {
	return d.Datastore.(datastore.DesiredMetadataReader).GetDesiredInlinePoliciesForStack(id)
}
func (d *desiredReadBarrier) LoadDesiredGeneratorsByStack(label string) ([]pkgmodel.Generator, error) {
	return d.Datastore.(datastore.DesiredMetadataReader).LoadDesiredGeneratorsByStack(label)
}
