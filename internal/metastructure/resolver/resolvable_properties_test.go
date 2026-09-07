// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/provenance"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

const sourceKsuid = "2abcdefghijklmnopqrstuvwxyz"

// consumerOf builds a resource whose Password property references property on
// the source resource.
func consumerOf(property string) pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:      "consumer",
		Type:       "Test::Consumer",
		Stack:      "default",
		Properties: json.RawMessage(`{"Password":{"$ref":"formae://` + sourceKsuid + `#/` + property + `"}}`),
	}
}

// stacksWith puts source in the shape LoadResolvablePropertiesFromStacks expects.
func stacksWith(source *pkgmodel.Resource) map[string][]*pkgmodel.Resource {
	return map[string][]*pkgmodel.Resource{"default": {source}}
}

// TestLoadResolvableProperties_SkipsHashedValue asserts that a value stored
// hashed at rest is not offered as a resolution: the digest is not what the
// source holds, so the reference must be left for execution-time resolution
// against the live source instead.
func TestLoadResolvableProperties_SkipsHashedValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "the-secret",
		Type:  "Test::Secret",
		Stack: "default",
		Properties: json.RawMessage(`{"SecretString":{` +
			`"$value":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",` +
			`"$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "SecretString")
	assert.False(t, found, "a hashed-at-rest value must not be offered as a resolution")
}

// TestLoadResolvableProperties_SkipsHashedReadOnlyValue is the same rule for a
// value that lives in ReadOnlyProperties, which takes precedence over
// Properties in the lookup.
func TestLoadResolvableProperties_SkipsHashedReadOnlyValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "the-secret",
		Type:  "Test::Secret",
		Stack: "default",
		ReadOnlyProperties: json.RawMessage(`{"GeneratedToken":{` +
			`"$value":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",` +
			`"$visibility":"Opaque","$hashed":true}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("GeneratedToken"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "GeneratedToken")
	assert.False(t, found, "a hashed-at-rest read-only value must not be offered as a resolution")
}

// TestLoadResolvableProperties_UsesOpaqueValueThatIsNotHashed asserts the rule
// keys on the value being a digest, not on the field being a credential: an
// opaque value still holding its plaintext resolves normally.
func TestLoadResolvableProperties_UsesOpaqueValueThatIsNotHashed(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:      sourceKsuid,
		Label:      "the-secret",
		Type:       "Test::Secret",
		Stack:      "default",
		Properties: json.RawMessage(`{"SecretString":{"$value":"live-value","$visibility":"Opaque"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "SecretString")
	require.True(t, found, "an opaque value that is not a digest must still resolve")
	assert.Equal(t, "live-value", value)
}

// TestLoadResolvableProperties_SkipsStructureHoldingAHashedValue asserts that a
// digest is refused wherever it sits: a reference naming a whole structure must
// not resolve to text with a hashed value buried inside it.
func TestLoadResolvableProperties_SkipsStructureHoldingAHashedValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "the-config",
		Type:  "Test::Config",
		Stack: "default",
		Properties: json.RawMessage(`{"Connection":{"Host":"db.internal","Password":{` +
			`"$value":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",` +
			`"$visibility":"Opaque","$hashed":true}}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Connection"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "Connection")
	assert.False(t, found, "a structure holding a hashed value must not be offered as a resolution")
}

// TestLoadResolvableProperties_SkipsValuelessReferenceEnvelope asserts that a
// source property which is itself an unresolved reference — what
// reference-don't-store leaves at rest — is not offered: its raw envelope text
// is not a value.
func TestLoadResolvableProperties_SkipsValuelessReferenceEnvelope(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:      sourceKsuid,
		Label:      "the-consumer",
		Type:       "Test::Consumer",
		Stack:      "default",
		Properties: json.RawMessage(`{"Token":{"$ref":"formae://someotherksuid#/Value","$visibility":"Opaque"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Token"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "Token")
	assert.False(t, found, "a reference envelope with no value must not be offered as a resolution")
}

// TestLoadResolvableProperties_UsesResolvedReferenceEnvelope is the chained
// reference: a source property that is itself a reference which has already
// been resolved still resolves, through the value it carries.
func TestLoadResolvableProperties_UsesResolvedReferenceEnvelope(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:      sourceKsuid,
		Label:      "the-subnet",
		Type:       "Test::Subnet",
		Stack:      "default",
		Properties: json.RawMessage(`{"VpcId":{"$ref":"formae://someotherksuid#/VpcId","$value":"vpc-123"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("VpcId"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "VpcId")
	require.True(t, found, "a resolved reference must still resolve through its value")
	assert.Equal(t, "vpc-123", value)
}

// TestLoadResolvableProperties_UsesPlainValue is the ordinary cross-resource
// reference: a plain persisted property resolves at planning time.
func TestLoadResolvableProperties_UsesPlainValue(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid:              sourceKsuid,
		Label:              "the-vpc",
		Type:               "Test::VPC",
		Stack:              "default",
		ReadOnlyProperties: json.RawMessage(`{"VpcId":"vpc-123"}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("VpcId"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "VpcId")
	require.True(t, found)
	assert.Equal(t, "vpc-123", value)
}

// A persisted literal the command does not move answers Stable and keeps the
// same value the string lookup returns, so both surfaces agree.
func TestResolvableProperties_AnswerAgreesWithGet(t *testing.T) {
	props := NewResolvableProperties()
	props.AddAnswer("k1", "Value", SourceAnswer{Kind: AnswerStable, Value: "hello"})

	v, ok := props.Get("k1", "Value")
	require.True(t, ok)
	assert.Equal(t, "hello", v)

	a, ok := props.Answer("k1", "Value")
	require.True(t, ok)
	assert.Equal(t, AnswerStable, a.Kind)
	assert.Equal(t, "hello", a.Value)
}

// When the command declares the source and its effective desired document
// carries a plain literal for the referenced property, that literal is the
// plan-time resolution, not the persisted row's stale value.
func TestLoadResolvableProperties_EffectiveDesiredLiteralWins(t *testing.T) {
	source := &pkgmodel.Resource{
		Label: "parent", Ksuid: "k-parent", Stack: "s",
		Properties: json.RawMessage(`{"Name": "p", "Value": "hello"}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"ParentRef": {"$ref": "formae://k-parent#/Value", "$value": "hello"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {source}}
	effective := map[string]json.RawMessage{
		"k-parent": json.RawMessage(`{"Name": "p", "Value": "world"}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	v, ok := props.Get("k-parent", "Value")
	require.True(t, ok)
	assert.Equal(t, "world", v, "the effective desired literal is the resolution")

	a, _ := props.Answer("k-parent", "Value")
	assert.Equal(t, AnswerResolved, a.Kind)
}

// An effective desired value that is a reference envelope, hashed at rest, or
// opaque is never materialized by this rule: behavior stays byte-identical to
// the persisted-row path (envelope: cached value; hashed/valueless: deferred).
func TestLoadResolvableProperties_EnvelopeAndHashedKeepTodaysBehavior(t *testing.T) {
	// The transitive root the "Chained" envelope points to: not declared in
	// effective, so the chain is unmoved and the outer envelope's own cached
	// value is what must answer — exercised recursively now, same as before.
	root := &pkgmodel.Resource{
		Label: "root", Ksuid: "k-root", Stack: "s",
		Properties: json.RawMessage(`{"V": "root-value"}`),
	}
	source := &pkgmodel.Resource{
		Label: "parent", Ksuid: "k-parent", Stack: "s",
		Properties: json.RawMessage(`{
			"Chained": {"$ref": "formae://k-root#/V", "$value": "cached"},
			"Secret":  {"$value": "digest-at-rest", "$hashed": true}
		}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"A": {"$ref": "formae://k-parent#/Chained"},
			"B": {"$ref": "formae://k-parent#/Secret"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {source, root}}
	effective := map[string]json.RawMessage{
		"k-parent": json.RawMessage(`{
			"Chained": {"$ref": "formae://k-root#/V"},
			"Secret":  "raw-new-secret-plaintext"
		}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	v, ok := props.Get("k-parent", "Chained")
	require.True(t, ok)
	assert.Equal(t, "cached", v, "an envelope source keeps the persisted cached value in this plan")

	_, ok = props.Get("k-parent", "Secret")
	assert.False(t, ok, "a hashed-at-rest source stays deferred; the raw desired plaintext must never be materialized")
}

// A source that marks the referenced property Opaque only in ReadOnlyProperties
// (never in Properties) must still be caught: the effective desired document's
// plain literal is not materialized, and resolution falls through to the
// persisted-row path instead.
func TestLoadResolvableProperties_EffectiveDesiredSkipsSourceOpaqueOnlyInReadOnlyProperties(t *testing.T) {
	source := &pkgmodel.Resource{
		Label: "parent", Ksuid: "k-parent", Stack: "s",
		Properties:         json.RawMessage(`{"Name": "p"}`),
		ReadOnlyProperties: json.RawMessage(`{"Token": {"$value": "persisted-token", "$visibility": "Opaque"}}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"Ref": {"$ref": "formae://k-parent#/Token", "$value": "persisted-token"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {source}}
	effective := map[string]json.RawMessage{
		"k-parent": json.RawMessage(`{"Name": "p", "Token": "resubmitted-token"}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	v, ok := props.Get("k-parent", "Token")
	require.True(t, ok, "the persisted-row path still resolves the opaque-but-not-hashed value")
	assert.Equal(t, "persisted-token", v,
		"the resubmitted effective plaintext must never be materialized for a source opaque only in ReadOnlyProperties")

	a, _ := props.Answer("k-parent", "Token")
	assert.Equal(t, AnswerStable, a.Kind, "fallthrough resolves via the persisted-row path, not the case-1 rule")
}

// A property that is inline-opaque only in the effective desired document —
// the persisted row lacks the property entirely, and the schema carries no
// opaque hint — must still never be materialized: the case-1 rule refuses it
// by the desired document's own $visibility marker.
func TestLoadResolvableProperties_EffectiveDesiredSkipsInlineOpaqueNotYetPersisted(t *testing.T) {
	source := &pkgmodel.Resource{
		Label: "parent", Ksuid: "k-parent", Stack: "s",
		Properties: json.RawMessage(`{"Name": "p"}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{"Ref": {"$ref": "formae://k-parent#/Secret"}}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {source}}
	effective := map[string]json.RawMessage{
		"k-parent": json.RawMessage(`{"Name": "p", "Secret": {"$value": "brand-new-plaintext", "$visibility": "Opaque"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	_, ok := props.Get("k-parent", "Secret")
	assert.False(t, ok, "an inline-opaque value that exists only in the effective desired document must never be materialized")
}

// A consumer references B, and B's own value is a reference to A. When the
// command changes A's literal, the consumer must resolve the value B will
// hold after this command: A's new literal, not B's stale cached value.
func TestLoadResolvableProperties_ChainRootLiteralWins(t *testing.T) {
	root := pkgmodel.Resource{
		Label: "root", Ksuid: "k-root", Stack: "s",
		Properties: json.RawMessage(`{"Name": "r", "Value": "blue"}`),
	}
	middle := pkgmodel.Resource{
		Label: "middle", Ksuid: "k-middle", Stack: "s",
		Properties: json.RawMessage(`{
			"Name": "m",
			"Value": {"$ref": "formae://k-root#/Value", "$value": "blue"}
		}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"Ref": {"$ref": "formae://k-middle#/Value", "$value": "blue"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {&root, &middle}}
	effective := map[string]json.RawMessage{
		"k-root":   json.RawMessage(`{"Name": "r", "Value": "green"}`),
		"k-middle": json.RawMessage(`{"Name": "m", "Value": {"$ref": "formae://k-root#/Value"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	v, ok := props.Get("k-middle", "Value")
	require.True(t, ok)
	assert.Equal(t, "green", v, "the chain root's new literal is the resolution, not the middle hop's stale cached value")

	a, _ := props.Answer("k-middle", "Value")
	assert.Equal(t, AnswerResolved, a.Kind)
}

// When nothing in the chain moves (the transitive root is not declared in the
// command), the middle resource's cached value is the last applied resolution
// and remains the answer.
func TestLoadResolvableProperties_ChainUnmovedKeepsCachedValue(t *testing.T) {
	root := pkgmodel.Resource{
		Label: "root", Ksuid: "k-root", Stack: "s",
		Properties: json.RawMessage(`{"Name": "r", "Value": "blue"}`),
	}
	middle := pkgmodel.Resource{
		Label: "middle", Ksuid: "k-middle", Stack: "s",
		Properties: json.RawMessage(`{
			"Name": "m",
			"Value": {"$ref": "formae://k-root#/Value", "$value": "blue"}
		}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"Ref": {"$ref": "formae://k-middle#/Value", "$value": "blue"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {&root, &middle}}
	effective := map[string]json.RawMessage{
		"k-middle": json.RawMessage(`{"Name": "m", "Value": {"$ref": "formae://k-root#/Value"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	v, ok := props.Get("k-middle", "Value")
	require.True(t, ok)
	assert.Equal(t, "blue", v, "the undeclared root means the middle hop's cached value remains the answer")

	a, _ := props.Answer("k-middle", "Value")
	assert.Equal(t, AnswerStable, a.Kind)
}

// An opaque marker anywhere on the hop keeps today's behavior: no recursion,
// the persisted fallthrough answers (cached value if present), and the raw
// desired plaintext of the transitive root is never consulted.
func TestLoadResolvableProperties_ChainOpaqueHopKeepsTodaysBehavior(t *testing.T) {
	root := pkgmodel.Resource{
		Label: "root", Ksuid: "k-root", Stack: "s",
		Properties: json.RawMessage(`{"Name": "r", "Secret": {"$value": "digest", "$hashed": true}}`),
	}
	middle := pkgmodel.Resource{
		Label: "middle", Ksuid: "k-middle", Stack: "s",
		Properties: json.RawMessage(`{
			"Name": "m",
			"Cred": {"$ref": "formae://k-root#/Secret", "$value": "cached-leaf", "$visibility": "Opaque"}
		}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"Ref": {"$ref": "formae://k-middle#/Cred", "$value": "cached-leaf", "$visibility": "Opaque"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {&root, &middle}}
	effective := map[string]json.RawMessage{
		"k-root":   json.RawMessage(`{"Name": "r", "Secret": "rotated-plaintext"}`),
		"k-middle": json.RawMessage(`{"Name": "m", "Cred": {"$ref": "formae://k-root#/Secret", "$visibility": "Opaque"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	v, ok := props.Get("k-middle", "Cred")
	require.True(t, ok)
	assert.Equal(t, "cached-leaf", v)
	assert.NotContains(t, v, "rotated-plaintext", "an opaque hop must never surface the transitive root's live plaintext")
}

// A middle hop that extracts a key from a JSON document resolves the
// extracted leaf of the root's new value, entirely in memory.
func TestLoadResolvableProperties_ChainJSONHopExtractsLeaf(t *testing.T) {
	root := pkgmodel.Resource{
		Label: "root", Ksuid: "k-root", Stack: "s",
		Properties: json.RawMessage(`{"Name": "r", "Doc": "{\"db\":{\"host\":\"old-host\"}}"}`),
	}
	middle := pkgmodel.Resource{
		Label: "middle", Ksuid: "k-middle", Stack: "s",
		Properties: json.RawMessage(`{
			"Name": "m",
			"Host": {"$ref": "formae://k-root#/Doc", "$value": "old-host", "$json": "db.host"}
		}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"Ref": {"$ref": "formae://k-middle#/Host", "$value": "old-host"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {&root, &middle}}
	effective := map[string]json.RawMessage{
		"k-root":   json.RawMessage(`{"Name": "r", "Doc": "{\"db\":{\"host\":\"new-host\"}}"}`),
		"k-middle": json.RawMessage(`{"Name": "m", "Host": {"$ref": "formae://k-root#/Doc", "$json": "db.host"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	v, ok := props.Get("k-middle", "Host")
	require.True(t, ok)
	assert.Equal(t, "new-host", v, "the JSON hop resolves the extracted leaf of the root's new value")
}

// Two references whose chains meet at the same root resolve independently: a
// diamond is not a cycle.
func TestLoadResolvableProperties_DiamondResolves(t *testing.T) {
	root := pkgmodel.Resource{
		Label: "root", Ksuid: "k-root", Stack: "s",
		Properties: json.RawMessage(`{"Name": "r", "Value": "blue"}`),
	}
	left := pkgmodel.Resource{
		Label: "left", Ksuid: "k-left", Stack: "s",
		Properties: json.RawMessage(`{
			"Name": "l",
			"Value": {"$ref": "formae://k-root#/Value", "$value": "blue"}
		}`),
	}
	right := pkgmodel.Resource{
		Label: "right", Ksuid: "k-right", Stack: "s",
		Properties: json.RawMessage(`{
			"Name": "rr",
			"Value": {"$ref": "formae://k-root#/Value", "$value": "blue"}
		}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"Left":  {"$ref": "formae://k-left#/Value", "$value": "blue"},
			"Right": {"$ref": "formae://k-right#/Value", "$value": "blue"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {&root, &left, &right}}
	effective := map[string]json.RawMessage{
		"k-root":  json.RawMessage(`{"Name": "r", "Value": "green"}`),
		"k-left":  json.RawMessage(`{"Name": "l", "Value": {"$ref": "formae://k-root#/Value"}}`),
		"k-right": json.RawMessage(`{"Name": "rr", "Value": {"$ref": "formae://k-root#/Value"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	leftVal, ok := props.Get("k-left", "Value")
	require.True(t, ok)
	assert.Equal(t, "green", leftVal)

	rightVal, ok := props.Get("k-right", "Value")
	require.True(t, ok)
	assert.Equal(t, "green", rightVal)
}

// A chain that loops back onto itself is a clean plan-time error naming the
// cycle, not a hang and not a stale resolution.
func TestLoadResolvableProperties_ReferenceCycleFails(t *testing.T) {
	a := pkgmodel.Resource{
		Label: "a", Ksuid: "k-a", Stack: "s",
		Properties: json.RawMessage(`{
			"Value": {"$ref": "formae://k-b#/Value", "$value": "x"}
		}`),
	}
	b := pkgmodel.Resource{
		Label: "b", Ksuid: "k-b", Stack: "s",
		Properties: json.RawMessage(`{
			"Value": {"$ref": "formae://k-a#/Value", "$value": "x"}
		}`),
	}
	consumer := pkgmodel.Resource{
		Label: "consumer", Ksuid: "k-consumer", Stack: "s",
		Properties: json.RawMessage(`{
			"Ref": {"$ref": "formae://k-a#/Value", "$value": "x"}
		}`),
	}
	all := map[string][]*pkgmodel.Resource{"s": {&a, &b}}
	effective := map[string]json.RawMessage{
		"k-a": json.RawMessage(`{"Value": {"$ref": "formae://k-b#/Value"}}`),
		"k-b": json.RawMessage(`{"Value": {"$ref": "formae://k-a#/Value"}}`),
	}

	_, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.Error(t, err)

	var cycleErr ReferenceCycleError
	require.True(t, errors.As(err, &cycleErr), "error must be a ReferenceCycleError")
	assert.GreaterOrEqual(t, len(cycleErr.Chain), 2, "the chain must name at least the repeated hop and its first occurrence")
}

const middleKsuid = "2middleghijklmnopqrstuvwxyz"

// A reference to a container field whose SCHEMA marks a nested descendant
// opaque (e.g. a hint on Config.Password while the reference names Config)
// must refuse plan-time materialization: the container holds a credential, so
// the whole referenced subtree is a credential for materialization purposes.
// This covers the first appearance of the value, before any inline
// $hashed/$visibility markers exist for the value-level walks to catch.
func TestLoadResolvableProperties_ContainerReference_RefusesDescendantOpaqueHint(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "holder",
		Type:  "Test::Config::Holder",
		Stack: "default",
		Schema: pkgmodel.Schema{
			Fields: []string{"Name", "Config"},
			Hints:  map[string]pkgmodel.FieldHint{"Config.Password": {Opaque: true}},
		},
		Properties: json.RawMessage(`{"Name":"s","Config":{"User":"u"}}`),
	}
	effective := map[string]json.RawMessage{
		sourceKsuid: json.RawMessage(`{"Name":"s","Config":{"User":"u","Password":"hunter2"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Config"), stacksWith(source), effective, nil)
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "Config")
	if found {
		assert.NotContains(t, value, "hunter2",
			"a reference to a container with an opaque descendant hint must not materialize the descendant's plaintext")
	}
}

// The same rule one hop out: a middle resource's reference envelope resolves
// the container and extracts the secret leaf via $json; the extracted
// plaintext must not reach a consumer referencing the middle's property.
func TestLoadResolvableProperties_ChainHopJSONExtraction_RefusesDescendantOpaqueHint(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "holder",
		Type:  "Test::Config::Holder",
		Stack: "default",
		Schema: pkgmodel.Schema{
			Fields: []string{"Name", "Config"},
			Hints:  map[string]pkgmodel.FieldHint{"Config.Password": {Opaque: true}},
		},
		Properties: json.RawMessage(`{"Name":"s","Config":{"User":"u"}}`),
	}
	middle := &pkgmodel.Resource{
		Ksuid:      middleKsuid,
		Label:      "middle",
		Type:       "Test::Config::Middle",
		Stack:      "default",
		Properties: json.RawMessage(`{"Ref":{"$ref":"formae://` + sourceKsuid + `#/Config"}}`),
	}
	consumer := pkgmodel.Resource{
		Label:      "consumer",
		Type:       "Test::Consumer",
		Stack:      "default",
		Properties: json.RawMessage(`{"Password":{"$ref":"formae://` + middleKsuid + `#/Ref"}}`),
	}
	all := map[string][]*pkgmodel.Resource{"default": {source, middle}}
	effective := map[string]json.RawMessage{
		sourceKsuid: json.RawMessage(`{"Name":"s","Config":{"User":"u","Password":"hunter2"}}`),
		middleKsuid: json.RawMessage(`{"Ref":{"$ref":"formae://` + sourceKsuid + `#/Config","$json":"Password"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
	require.NoError(t, err)

	value, found := props.Get(middleKsuid, "Ref")
	if found {
		assert.NotContains(t, value, "hunter2",
			"a chain hop's JSON extraction must not isolate an opaque descendant's plaintext")
	}
}

// A sibling field with no opaque descendant still resolves from effective
// desired state: the descendant rule must not over-refuse.
func TestLoadResolvableProperties_SiblingOfOpaqueDescendant_StillResolves(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "holder",
		Type:  "Test::Config::Holder",
		Stack: "default",
		Schema: pkgmodel.Schema{
			Fields: []string{"Name", "Config"},
			Hints:  map[string]pkgmodel.FieldHint{"Config.Password": {Opaque: true}},
		},
		Properties: json.RawMessage(`{"Name":"old","Config":{"User":"u"}}`),
	}
	effective := map[string]json.RawMessage{
		sourceKsuid: json.RawMessage(`{"Name":"new","Config":{"User":"u","Password":"hunter2"}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Name"), stacksWith(source), effective, nil)
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "Name")
	require.True(t, found, "a sibling of an opaque descendant must still resolve")
	assert.Equal(t, "new", value)
}

// A reference BELOW a nested opaque hint (the hint on Config.Password, the
// reference into Config.Password.value) is a key into a nested map-shaped
// secret: every ancestor of the referenced path must be consulted, not only
// its top-level root.
func TestLoadResolvableProperties_ReferenceBelowNestedOpaqueHint_Refused(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "holder",
		Type:  "Test::Config::Holder",
		Stack: "default",
		Schema: pkgmodel.Schema{
			Fields: []string{"Name", "Config"},
			Hints:  map[string]pkgmodel.FieldHint{"Config.Password": {Opaque: true}},
		},
		Properties: json.RawMessage(`{"Name":"s","Config":{"User":"u"}}`),
	}
	effective := map[string]json.RawMessage{
		sourceKsuid: json.RawMessage(`{"Name":"s","Config":{"User":"u","Password":{"value":"hunter2"}}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Config.Password.value"), stacksWith(source), effective, nil)
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "Config.Password.value")
	if found {
		assert.NotContains(t, value, "hunter2",
			"a reference below a nested opaque hint must not materialize the secret")
	}
}

// A reference below a NON-opaque sibling of the hinted field still resolves.
func TestLoadResolvableProperties_ReferenceBelowNonOpaqueSibling_StillResolves(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid,
		Label: "holder",
		Type:  "Test::Config::Holder",
		Stack: "default",
		Schema: pkgmodel.Schema{
			Fields: []string{"Name", "Config"},
			Hints:  map[string]pkgmodel.FieldHint{"Config.Password": {Opaque: true}},
		},
		Properties: json.RawMessage(`{"Name":"s","Config":{"Meta":{"region":"old"}}}`),
	}
	effective := map[string]json.RawMessage{
		sourceKsuid: json.RawMessage(`{"Name":"s","Config":{"Meta":{"region":"new"},"Password":{"value":"hunter2"}}}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("Config.Meta.region"), stacksWith(source), effective, nil)
	require.NoError(t, err)

	value, found := props.Get(sourceKsuid, "Config.Meta.region")
	require.True(t, found, "a reference below a non-opaque sibling must still resolve")
	assert.Equal(t, "new", value)
}

// Every opaque arm of plan-time classification must answer with a retained,
// opacity-flagged, digest-carrying deferred answer: the value is still never
// handed out (Get refuses), but downstream classification needs to know WHY
// and against WHICH source digest to compare.
func TestLoadResolvableProperties_OpaqueAnswersCarryDigests(t *testing.T) {
	hex64 := pkgmodel.ComputeValueHash("stored-secret")

	t.Run("declared bare schema-opaque value", func(t *testing.T) {
		source := &pkgmodel.Resource{
			Ksuid: sourceKsuid, Label: "s", Type: "Test::Secret", Stack: "default",
			Schema: pkgmodel.Schema{
				Fields: []string{"Name", "SecretString"},
				Hints:  map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}},
			},
			Properties: json.RawMessage(`{"Name":"s"}`),
		}
		effective := map[string]json.RawMessage{
			sourceKsuid: json.RawMessage(`{"Name":"s","SecretString":"hunter2"}`),
		}
		props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), effective, nil)
		require.NoError(t, err)

		_, found := props.Get(sourceKsuid, "SecretString")
		assert.False(t, found, "opaque values are never handed out")

		a, ok := props.Answer(sourceKsuid, "SecretString")
		require.True(t, ok, "the deferred answer must be retained")
		assert.Equal(t, AnswerDeferred, a.Kind)
		assert.True(t, a.Opaque)
		assert.Equal(t, provenance.DigestOfString("hunter2"), a.SourceRootDigest)
		assert.NotEmpty(t, a.SourceRaw())
	})

	t.Run("declared opaque envelope unwraps before digesting", func(t *testing.T) {
		source := &pkgmodel.Resource{
			Ksuid: sourceKsuid, Label: "s", Type: "Test::Secret", Stack: "default",
			Properties: json.RawMessage(`{"Name":"s"}`),
		}
		effective := map[string]json.RawMessage{
			sourceKsuid: json.RawMessage(`{"SecretString":{"$value":"hunter2","$visibility":"Opaque","$strategy":"Update"}}`),
		}
		props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), effective, nil)
		require.NoError(t, err)

		a, ok := props.Answer(sourceKsuid, "SecretString")
		require.True(t, ok)
		assert.True(t, a.Opaque)
		assert.Equal(t, provenance.DigestOfString("hunter2"), a.SourceRootDigest,
			"the digest is of the unwrapped value, never the envelope")
	})

	t.Run("declared hashed envelope adapts the stored digest", func(t *testing.T) {
		source := &pkgmodel.Resource{
			Ksuid: sourceKsuid, Label: "s", Type: "Test::Secret", Stack: "default",
			Properties: json.RawMessage(`{"Name":"s"}`),
		}
		effective := map[string]json.RawMessage{
			sourceKsuid: json.RawMessage(`{"SecretString":{"$value":"` + hex64 + `","$visibility":"Opaque","$hashed":true}}`),
		}
		props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), effective, nil)
		require.NoError(t, err)

		a, ok := props.Answer(sourceKsuid, "SecretString")
		require.True(t, ok)
		assert.True(t, a.Opaque)
		assert.Equal(t, provenance.FromStored(hex64), a.SourceRootDigest)
	})

	t.Run("undeclared source answers from the stored digest", func(t *testing.T) {
		source := &pkgmodel.Resource{
			Ksuid: sourceKsuid, Label: "s", Type: "Test::Secret", Stack: "default",
			Properties: json.RawMessage(`{"SecretString":{"$value":"` + hex64 + `","$visibility":"Opaque","$strategy":"Update","$hashed":true}}`),
		}
		props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), nil, nil)
		require.NoError(t, err)

		a, ok := props.Answer(sourceKsuid, "SecretString")
		require.True(t, ok)
		assert.Equal(t, AnswerDeferred, a.Kind)
		assert.True(t, a.Opaque)
		assert.Equal(t, provenance.FromStored(hex64), a.SourceRootDigest)
	})

	t.Run("undeclared source with no stored digest is unknown", func(t *testing.T) {
		source := &pkgmodel.Resource{
			Ksuid: sourceKsuid, Label: "s", Type: "Test::Secret", Stack: "default",
			Schema: pkgmodel.Schema{
				Fields: []string{"SecretString"},
				Hints:  map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}},
			},
			Properties: json.RawMessage(`{"Name":"s"}`),
		}
		props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), nil, nil)
		require.NoError(t, err)

		a, ok := props.Answer(sourceKsuid, "SecretString")
		require.True(t, ok)
		assert.True(t, a.Opaque)
		assert.Empty(t, a.SourceRootDigest)
	})

	t.Run("chain to an opaque root carries the root digest", func(t *testing.T) {
		source := &pkgmodel.Resource{
			Ksuid: sourceKsuid, Label: "s", Type: "Test::Secret", Stack: "default",
			Schema: pkgmodel.Schema{
				Fields: []string{"SecretString"},
				Hints:  map[string]pkgmodel.FieldHint{"SecretString": {Opaque: true}},
			},
			Properties: json.RawMessage(`{"Name":"s"}`),
		}
		middle := &pkgmodel.Resource{
			Ksuid: middleKsuid, Label: "m", Type: "Test::Middle", Stack: "default",
			Properties: json.RawMessage(`{"Ref":{"$ref":"formae://` + sourceKsuid + `#/SecretString"}}`),
		}
		consumer := pkgmodel.Resource{
			Label: "consumer", Type: "Test::Consumer", Stack: "default",
			Properties: json.RawMessage(`{"Password":{"$ref":"formae://` + middleKsuid + `#/Ref"}}`),
		}
		all := map[string][]*pkgmodel.Resource{"default": {source, middle}}
		effective := map[string]json.RawMessage{
			sourceKsuid: json.RawMessage(`{"SecretString":"hunter2"}`),
			middleKsuid: json.RawMessage(`{"Ref":{"$ref":"formae://` + sourceKsuid + `#/SecretString"}}`),
		}
		props, err := LoadResolvablePropertiesFromStacks(consumer, all, effective, nil)
		require.NoError(t, err)

		a, ok := props.Answer(middleKsuid, "Ref")
		require.True(t, ok)
		assert.True(t, a.Opaque, "opacity is inherited through the chain")
		assert.Equal(t, provenance.DigestOfString("hunter2"), a.SourceRootDigest)
		_, found := props.Get(middleKsuid, "Ref")
		assert.False(t, found)
	})
}

const generatorKsuid = "2genabcdefghijklmnopqrstuvw"

// translatedGen is the shape a $gen envelope has after command translation
// has resolved the generator label to its KSUID.
const translatedGen = `{"$gen":true,"$generator":"` + generatorKsuid + `","$output":"value","$visibility":"Opaque"}`

// genConsumer builds a resource whose Password property is bound to the
// generator's "value" output.
func genConsumer() pkgmodel.Resource {
	return pkgmodel.Resource{
		Label:      "consumer",
		Type:       "Test::Consumer",
		Stack:      "default",
		Properties: json.RawMessage(`{"Password":` + translatedGen + `}`),
	}
}

// generationOf builds a generator identity holding generationID, drawn under
// drawn.
func generationOf(t *testing.T, generationID string, drawn pkgmodel.Generator) pkgmodel.GeneratorIdentity {
	t.Helper()
	identity := pkgmodel.GeneratorIdentity{ID: generatorKsuid, GenerationID: generationID}
	if drawn != nil {
		spec, err := json.Marshal(drawn)
		require.NoError(t, err)
		identity.GenerationSpec = spec
	}
	return identity
}

// answering returns a lookup that answers for every generator KSUID.
func answering(identity pkgmodel.GeneratorIdentity, desired pkgmodel.Generator) GeneratorGenerationLookup {
	return func(string) (pkgmodel.GeneratorIdentity, pkgmodel.Generator) { return identity, desired }
}

// password builds a password generator spec, applying mutate to the baseline.
func password(mutate func(*pkgmodel.PasswordGenerator)) *pkgmodel.PasswordGenerator {
	g := &pkgmodel.PasswordGenerator{
		Label: "db-password", Stack: "default",
		Length: 32, Uppercase: true, Lowercase: true, Digits: true,
		RequireEachIncludedType: true,
	}
	mutate(g)
	return g
}

// A $gen names a generator, which has no resource row, so answering it must
// not go looking for one.
func TestLoadResolvableProperties_GenSourceDoesNotRequireAResourceRow(t *testing.T) {
	spec := password(func(*pkgmodel.PasswordGenerator) {})
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(generationOf(t, "gen-1", spec), spec))
	require.NoError(t, err)

	_, ok := props.Answer(generatorKsuid, "value")
	assert.True(t, ok, "the generator's output must be answered without a resource row")
}

// A generated value is opaque by construction, so the occurrence classifier
// governs it rather than being short-circuited at R1.
func TestLoadResolvableProperties_GenAnswerIsOpaque(t *testing.T) {
	spec := password(func(*pkgmodel.PasswordGenerator) {})
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(generationOf(t, "gen-1", spec), spec))
	require.NoError(t, err)

	a, ok := props.Answer(generatorKsuid, "value")
	require.True(t, ok)
	assert.True(t, a.Opaque)
	assert.Equal(t, AnswerDeferred, a.Kind, "a generated value is drawn at execution, never at plan time")
	_, found := props.Get(generatorKsuid, "value")
	assert.False(t, found, "a deferred answer never hands out a value")
}

// The generation the generator currently holds is what a $gen occurrence's
// movement is decided against, so it sits in the source root digest slot.
func TestLoadResolvableProperties_GenDigestIsTheGenerationIdentity(t *testing.T) {
	spec := password(func(*pkgmodel.PasswordGenerator) {})
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(generationOf(t, "gen-1", spec), spec))
	require.NoError(t, err)

	a, _ := props.Answer(generatorKsuid, "value")
	assert.Equal(t, provenance.DigestOfString("gen-1"), a.SourceRootDigest)
	assert.True(t, provenance.Valid(a.SourceRootDigest))
}

// A generator holding no generation yet has no comparable identity, so its
// movement is unknown and the first apply materializes a value.
func TestLoadResolvableProperties_GenWithNoGenerationHasNoDigest(t *testing.T) {
	spec := password(func(*pkgmodel.PasswordGenerator) {})
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(pkgmodel.GeneratorIdentity{ID: generatorKsuid}, spec))
	require.NoError(t, err)

	a, ok := props.Answer(generatorKsuid, "value")
	require.True(t, ok)
	assert.True(t, a.Opaque)
	assert.Empty(t, a.SourceRootDigest)
}

// A generator with no row at all answers the same way as one holding no
// generation: nothing comparable, so the occurrence is planned.
func TestLoadResolvableProperties_UnknownGeneratorHasNoDigest(t *testing.T) {
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(pkgmodel.GeneratorIdentity{}, nil))
	require.NoError(t, err)

	a, ok := props.Answer(generatorKsuid, "value")
	require.True(t, ok)
	assert.Empty(t, a.SourceRootDigest)
}

// A spec edit the held generation can no longer satisfy reports the
// generation as moved, even though its identity has not changed.
func TestLoadResolvableProperties_GenWithUnsatisfiedSpecHasNoDigest(t *testing.T) {
	drawn := password(func(*pkgmodel.PasswordGenerator) {})
	desired := password(func(g *pkgmodel.PasswordGenerator) { g.Length = 64 })
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(generationOf(t, "gen-1", drawn), desired))
	require.NoError(t, err)

	a, _ := props.Answer(generatorKsuid, "value")
	assert.Empty(t, a.SourceRootDigest)
}

// A spec edit the held generation still satisfies leaves the generation
// where it is, so the digest still identifies it.
func TestLoadResolvableProperties_GenWithSatisfiedSpecEditKeepsItsDigest(t *testing.T) {
	drawn := password(func(g *pkgmodel.PasswordGenerator) { g.Symbols = false; g.RequireEachIncludedType = false })
	desired := password(func(g *pkgmodel.PasswordGenerator) { g.Symbols = true; g.RequireEachIncludedType = false })
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(generationOf(t, "gen-1", drawn), desired))
	require.NoError(t, err)

	a, _ := props.Answer(generatorKsuid, "value")
	assert.Equal(t, provenance.DigestOfString("gen-1"), a.SourceRootDigest)
}

// A stored drawing spec that cannot be parsed cannot be proven to still be
// satisfied, so the generation is reported as moved rather than failing the
// plan outright.
func TestLoadResolvableProperties_GenWithUnreadableDrawnSpecHasNoDigest(t *testing.T) {
	identity := pkgmodel.GeneratorIdentity{
		ID:             generatorKsuid,
		GenerationID:   "gen-1",
		GenerationSpec: json.RawMessage(`{"Type":"quantum-flux"}`),
	}
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(identity, password(func(*pkgmodel.PasswordGenerator) {})))
	require.NoError(t, err)

	a, _ := props.Answer(generatorKsuid, "value")
	assert.Empty(t, a.SourceRootDigest)
}

// A command that does not redeclare the generator cannot have edited its
// spec, so the held generation stands without a satisfaction check.
func TestLoadResolvableProperties_GenNotRedeclaredKeepsItsDigest(t *testing.T) {
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil,
		answering(generationOf(t, "gen-1", password(func(*pkgmodel.PasswordGenerator) {})), nil))
	require.NoError(t, err)

	a, _ := props.Answer(generatorKsuid, "value")
	assert.Equal(t, provenance.DigestOfString("gen-1"), a.SourceRootDigest)
}

// Without a lookup there is nothing to decide movement against, so the
// occurrence is answered as unknown rather than left unanswered.
func TestLoadResolvableProperties_GenWithoutALookupHasNoDigest(t *testing.T) {
	props, err := LoadResolvablePropertiesFromStacks(genConsumer(), nil, nil, nil)
	require.NoError(t, err)

	a, ok := props.Answer(generatorKsuid, "value")
	require.True(t, ok)
	assert.True(t, a.Opaque)
	assert.Empty(t, a.SourceRootDigest)
}

// An untranslated $gen names no generator KSUID, so there is no identity to
// key an answer on and the occurrence falls to fail-closed normalization.
func TestLoadResolvableProperties_UntranslatedGenIsNotAnswered(t *testing.T) {
	consumer := pkgmodel.Resource{
		Label: "consumer", Type: "Test::Consumer", Stack: "default",
		Properties: json.RawMessage(`{"Password":{"$gen":true,"$label":"db-password","$stack":"default","$output":"value"}}`),
	}
	props, err := LoadResolvablePropertiesFromStacks(consumer, nil, nil,
		answering(generationOf(t, "gen-1", password(func(*pkgmodel.PasswordGenerator) {})), nil))
	require.NoError(t, err)

	_, ok := props.Answer(generatorKsuid, "value")
	assert.False(t, ok)
}

// A stored $gen envelope carrying no drawn value yet is a binding, not a
// value, so a reference to the property it sits at must not resolve to the
// envelope's own text.
func TestLoadResolvableProperties_SkipsValuelessGenEnvelope(t *testing.T) {
	source := &pkgmodel.Resource{
		Ksuid: sourceKsuid, Label: "the-secret", Type: "Test::Secret", Stack: "default",
		Properties: json.RawMessage(`{"SecretString":` + translatedGen + `}`),
	}

	props, err := LoadResolvablePropertiesFromStacks(consumerOf("SecretString"), stacksWith(source), nil, nil)
	require.NoError(t, err)

	_, found := props.Get(sourceKsuid, "SecretString")
	assert.False(t, found, "a generator binding is not a value a reference may resolve to")
}

// A $gen sitting under a map key that contains a dot is answered like any
// other. The walk's dotted path cannot address such a key, so the envelope's
// generator must be read off the node itself.
func TestLoadResolvableProperties_GenUnderADottedMapKeyIsAnswered(t *testing.T) {
	consumer := pkgmodel.Resource{
		Label: "consumer", Type: "Test::Consumer", Stack: "default",
		Properties: json.RawMessage(`{"Tags":{"app.pw":` + translatedGen + `}}`),
	}
	spec := password(func(*pkgmodel.PasswordGenerator) {})

	props, err := LoadResolvablePropertiesFromStacks(consumer, nil, nil,
		answering(generationOf(t, "gen-1", spec), spec))
	require.NoError(t, err)

	a, ok := props.Answer(generatorKsuid, "value")
	require.True(t, ok, "a dotted map key must not hide the binding")
	assert.True(t, a.Opaque)
	assert.Equal(t, provenance.DigestOfString("gen-1"), a.SourceRootDigest)
}
