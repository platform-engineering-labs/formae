// © 2026 Platform Engineering Labs Inc.
// SPDX-License-Identifier: FSL-1.1-ALv2

package conformance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ergo.services/ergo/gen"
	"github.com/platform-engineering-labs/formae/pkg/plugin-conformance-tests/testutil"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/tidwall/gjson"
)

// Capture the coordinator request at the RPC boundary without launching a cloud plugin.
type unmanagedCreateNode struct {
	gen.Node
	requests []CreateResourceRequest
}

func (n *unmanagedCreateNode) Name() gen.Atom { return "unmanaged-values@localhost" }
func (n *unmanagedCreateNode) Send(_ any, message any) error {
	call, ok := message.(testutil.TestCall[any, any])
	if !ok {
		return fmt.Errorf("unexpected call %T", message)
	}
	req, ok := call.Request.(CreateResourceRequest)
	if !ok {
		return fmt.Errorf("unexpected request %T", call.Request)
	}
	n.requests = append(n.requests, req)
	go func() {
		call.Response <- CreateResourceResult{InitialProgress: resource.ProgressResult{
			OperationStatus: resource.OperationStatusSuccess, NativeID: req.Label, ResourceProperties: req.Properties,
		}}
	}()
	return nil
}
func TestCreateAllUnmanagedResourcesUnwrapsValues(t *testing.T) {
	n := &unmanagedCreateNode{}
	h := &TestHarness{t: t, ergoNode: n, lastPluginNamespace: "Test"}
	input := `{"Targets":[{"Label":"test"}],"Resources":[{"Label":"source","Type":"Test::Thing","Properties":{"version":{"$value":"1.34","$strategy":"SetOnce"},"items":[{"$value":false}],"cfg":{"$value":{"enabled":{"$value":true}}}}},{"Label":"consumer","Type":"Test::Thing","Properties":{"from":{"$res":true,"$label":"source","$type":"Test::Thing","$property":"version"},"version":{"$value":"2","$strategy":"SetOnce"}}}]}`
	created, err := h.CreateAllUnmanagedResources(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || len(n.requests) != 2 {
		t.Fatalf("created %d, requests %d", len(created), len(n.requests))
	}
	for _, req := range n.requests {
		if strings.Contains(string(req.Properties), `"$value"`) || strings.Contains(string(req.Properties), `"$res"`) {
			t.Fatalf("envelope reached plugin: %s", req.Properties)
		}
	}
	if gjson.GetBytes(n.requests[0].Properties, "version").String() != "1.34" || !gjson.GetBytes(n.requests[0].Properties, "cfg.enabled").Bool() || gjson.GetBytes(n.requests[0].Properties, "items.0").Raw != "false" {
		t.Fatalf("wrong source properties: %s", n.requests[0].Properties)
	}
	if gjson.GetBytes(n.requests[1].Properties, "from").String() != "1.34" {
		t.Fatalf("wrong resolved value: %s", n.requests[1].Properties)
	}
}
func TestCreateAllUnmanagedResourcesRejectsStoredHash(t *testing.T) {
	n := &unmanagedCreateNode{}
	h := &TestHarness{t: t, ergoNode: n, lastPluginNamespace: "Test"}
	input := `{"Targets":[{"Label":"test"}],"Resources":[{"Label":"source","Type":"Test::Thing","Properties":{"password":{"$value":"private-digest","$hashed":true,"$visibility":"Opaque"}}}]}`
	_, err := h.CreateAllUnmanagedResources(input)
	if err == nil {
		t.Fatal("stored hash accepted for create")
	}
	if strings.Contains(err.Error(), "private-digest") {
		t.Fatal("error exposes stored value")
	}
	if len(n.requests) != 0 {
		t.Fatal("stored hash reached plugin")
	}
}

func TestFlattenValuesPreservesLargeNumbers(t *testing.T) {
	h := &TestHarness{t: t}
	got, err := h.flattenFormaeValuesInProperties(json.RawMessage(`{"value":{"$value":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(got, "value").Raw != "9007199254740993" {
		t.Fatalf("numeric value changed: %s", got)
	}
}
