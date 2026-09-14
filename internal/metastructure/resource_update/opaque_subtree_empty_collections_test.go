// © 2026 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resource_update

import (
	"encoding/json"
	"strings"
	"testing"

	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

// The Create path never diffs, so the only stripper standing between a forma and
// the plugin is convertResourceForPlugin. It erases empty collections anywhere
// in the properties, which for K8S::Custom::Resource would erase the CRD body's
// meaning: `spec: {selfSigned: {}}` IS a self-signed cert-manager issuer, and
// stripped to `spec: {}` the apiserver rejects it with "at least one issuer must
// be configured", so the resource can never be created. See PLA-710.
//
// What stops that is the preserveEmptyValues hint, and the hint is why this
// fixture sets it: `K8S::Custom::Resource.spec` carries
// `@k8s.FieldHint { updateMethod = "Atomic"; preserveEmptyValues = true }`
// in the shipped schema, so this mirrors the real declaration.
//
// The hint is load-bearing rather than decorative. Opacity cannot be inferred
// from the Schema that reaches Go: Fields and Hints cross the boundary, the Pkl
// type does not, so a bare "spec" with no "spec.*" entries is indistinguishable
// from a schema that simply lists its top-level fields. See
// TestConvertResourceForPlugin_PreserveEmptyFieldSurvives, which pins an
// unhinted field with no declared interior as still stripped. A plugin owning an
// opaque field has to say so.
func TestConvertResourceForPlugin_KeepsEmptyMapBelowUndeclaredSubtree(t *testing.T) {
	res := pkgmodel.Resource{
		Label: "selfsigned",
		Type:  "K8S::Custom::Resource",
		Schema: pkgmodel.Schema{
			Identifier: "$.formaeId",
			Fields: []string{
				"apiVersion", "kind", "metadata", "spec", "formaeId",
				"metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations",
			},
			Hints: map[string]pkgmodel.FieldHint{
				"apiVersion": {Required: true},
				"kind":       {Required: true},
				"metadata":   {Required: true},
				"spec":       {PreserveEmptyValues: true},
			},
		},
		Properties: json.RawMessage(`{"apiVersion":"cert-manager.io/v1","kind":"ClusterIssuer",` +
			`"metadata":{"name":"selfsigned"},"spec":{"selfSigned":{}},` +
			`"formaeId":"cert-manager.io/v1/ClusterIssuer//selfsigned"}`),
	}

	converted, err := convertResourceForPlugin(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(converted.Properties), "selfSigned") {
		t.Fatalf("spec.selfSigned = {} is the issuer's type, not a PKL rendering artifact; "+
			"the plugin must receive it, got %s", string(converted.Properties))
	}
}

// The fence: where the schema declares the interior, an unset nullable
// collection really is a PKL artifact and must still be stripped, or providers
// that reject empty handler objects (the K8s probe case this stripping was added
// for) start failing again.
func TestConvertResourceForPlugin_StillStripsArtifactUnderDeclaredSubtree(t *testing.T) {
	res := pkgmodel.Resource{
		Label: "pod",
		Type:  "K8S::Core::Pod",
		Schema: pkgmodel.Schema{
			Fields: []string{
				"metadata", "metadata.name",
				"spec", "spec.containers", "spec.containers.name",
				"spec.containers.livenessProbe", "spec.nodeSelector",
			},
			Hints: map[string]pkgmodel.FieldHint{"metadata": {Required: true}, "spec": {}},
		},
		Properties: json.RawMessage(`{"metadata":{"name":"p"},` +
			`"spec":{"containers":[{"name":"app","livenessProbe":{}}],"nodeSelector":{}}}`),
	}

	converted, err := convertResourceForPlugin(res)
	if err != nil {
		t.Fatal(err)
	}
	got := string(converted.Properties)
	if strings.Contains(got, "livenessProbe") {
		t.Errorf("an unset declared Mapping must stay stripped, got %s", got)
	}
	if strings.Contains(got, "nodeSelector") {
		t.Errorf("an unset declared Mapping must stay stripped, got %s", got)
	}
}
