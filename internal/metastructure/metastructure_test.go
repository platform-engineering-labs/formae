// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package metastructure

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ergo.services/ergo/gen"
	_ "github.com/platform-engineering-labs/formae/internal/datastore/all"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReplaceKSUIDs_NestedRefInArrays(t *testing.T) {
	// This test verifies that $ref objects nested inside arrays are properly
	// converted to $res objects. This is the case for resources like
	// GCP::Compute::Instance where disks[].source and networkInterfaces[].network
	// contain $ref objects.

	tests := []struct {
		name           string
		inputJSON      string
		ksuidToTriplet map[string]pkgmodel.TripletKey
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "converts $ref in top-level property",
			inputJSON: `{
				"network": {
					"$ref": "formae://abc123#/selfLink",
					"$value": "https://example.com/network"
				}
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"abc123": {Stack: "my-stack", Label: "my-network", Type: "GCP::Compute::Network"},
			},
			wantContains:   []string{`"$res":true`, `"$label":"my-network"`, `"$stack":"my-stack"`, `"$type":"GCP::Compute::Network"`, `"$property":"selfLink"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "converts $ref nested in array of objects",
			inputJSON: `{
				"disks": [
					{
						"boot": true,
						"source": {
							"$ref": "formae://disk123#/selfLink",
							"$value": "https://example.com/disk"
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"disk123": {Stack: "my-stack", Label: "my-disk", Type: "GCP::Compute::Disk"},
			},
			wantContains:   []string{`"$res":true`, `"$label":"my-disk"`, `"$stack":"my-stack"`, `"$type":"GCP::Compute::Disk"`, `"$property":"selfLink"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "converts multiple $ref objects in array",
			inputJSON: `{
				"networkInterfaces": [
					{
						"name": "nic0",
						"network": {
							"$ref": "formae://net123#/selfLink",
							"$value": "https://example.com/network"
						},
						"subnetwork": {
							"$ref": "formae://subnet123#/selfLink",
							"$value": "https://example.com/subnet"
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"net123":    {Stack: "my-stack", Label: "my-network", Type: "GCP::Compute::Network"},
				"subnet123": {Stack: "my-stack", Label: "my-subnet", Type: "GCP::Compute::Subnetwork"},
			},
			wantContains:   []string{`"$label":"my-network"`, `"$label":"my-subnet"`, `"$type":"GCP::Compute::Network"`, `"$type":"GCP::Compute::Subnetwork"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "converts deeply nested $ref in array",
			inputJSON: `{
				"items": [
					{
						"nested": {
							"deep": {
								"ref": {
									"$ref": "formae://deep123#/prop",
									"$value": "deep-value"
								}
							}
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"deep123": {Stack: "deep-stack", Label: "deep-label", Type: "Deep::Type"},
			},
			wantContains:   []string{`"$res":true`, `"$label":"deep-label"`, `"$stack":"deep-stack"`},
			wantNotContain: []string{`"$ref"`},
		},
		{
			name: "handles mix of arrays and nested objects with refs",
			inputJSON: `{
				"topLevel": {
					"$ref": "formae://top123#/topProp",
					"$value": "top-value"
				},
				"arrayField": [
					{
						"arrayNested": {
							"$ref": "formae://arr123#/arrProp",
							"$value": "arr-value"
						}
					}
				]
			}`,
			ksuidToTriplet: map[string]pkgmodel.TripletKey{
				"top123": {Stack: "stack1", Label: "label1", Type: "Type1"},
				"arr123": {Stack: "stack2", Label: "label2", Type: "Type2"},
			},
			wantContains:   []string{`"$label":"label1"`, `"$label":"label2"`, `"$type":"Type1"`, `"$type":"Type2"`},
			wantNotContain: []string{`"$ref"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := replaceKSUIDs(tt.inputJSON, tt.ksuidToTriplet)

			for _, want := range tt.wantContains {
				assert.Contains(t, result, want, "result should contain %s", want)
			}

			for _, notWant := range tt.wantNotContain {
				assert.NotContains(t, result, notWant, "result should not contain %s", notWant)
			}
		})
	}
}

func TestExtractKSUIDs_NestedRefInArrays(t *testing.T) {
	// This test verifies that KSUIDs from $ref objects nested inside arrays
	// are properly extracted. This is a prerequisite for replaceKSUIDs to work.

	tests := []struct {
		name         string
		inputJSON    string
		wantKSUIDs   []string
		wantNotFound []string
	}{
		{
			name: "extracts KSUID from top-level $ref",
			inputJSON: `{
				"network": {
					"$ref": "formae://abc123#/selfLink",
					"$value": "https://example.com/network"
				}
			}`,
			wantKSUIDs: []string{"abc123"},
		},
		{
			name: "extracts KSUID from $ref nested in array",
			inputJSON: `{
				"disks": [
					{
						"boot": true,
						"source": {
							"$ref": "formae://disk123#/selfLink",
							"$value": "https://example.com/disk"
						}
					}
				]
			}`,
			wantKSUIDs: []string{"disk123"},
		},
		{
			name: "extracts multiple KSUIDs from array items",
			inputJSON: `{
				"networkInterfaces": [
					{
						"network": {
							"$ref": "formae://net123#/selfLink",
							"$value": "https://example.com/network"
						},
						"subnetwork": {
							"$ref": "formae://subnet456#/selfLink",
							"$value": "https://example.com/subnet"
						}
					}
				]
			}`,
			wantKSUIDs: []string{"net123", "subnet456"},
		},
		{
			name: "extracts KSUID from deeply nested array",
			inputJSON: `{
				"items": [
					{
						"nested": {
							"deep": {
								"ref": {
									"$ref": "formae://deep789#/prop",
									"$value": "deep-value"
								}
							}
						}
					}
				]
			}`,
			wantKSUIDs: []string{"deep789"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ksuidSet := make(map[string]struct{})
			extractKSUIDs(tt.inputJSON, ksuidSet)

			for _, want := range tt.wantKSUIDs {
				_, found := ksuidSet[want]
				assert.True(t, found, "should have extracted KSUID %s", want)
			}

			for _, notWant := range tt.wantNotFound {
				_, found := ksuidSet[notWant]
				assert.False(t, found, "should not have extracted KSUID %s", notWant)
			}
		})
	}
}

func TestMetastructure_NetworkingEnabled(t *testing.T) {
	cfg := &pkgmodel.Config{
		Agent: pkgmodel.AgentConfig{
			Server: pkgmodel.ServerConfig{
				Nodename: "test-agent",
				Hostname: "localhost",
				Secret:   "secret",
			},
		},
	}

	m, err := NewMetastructure(context.Background(), cfg, nil, "test")
	require.NoError(t, err)
	require.NotNil(t, m)

	// Verify networking is enabled
	assert.NotEqual(t, gen.NetworkModeDisabled, m.options.Network.Mode,
		"Network mode should not be disabled")
	assert.Equal(t, gen.NetworkModeEnabled, m.options.Network.Mode,
		"Network mode should be enabled")

	// Verify cookie is set from config
	assert.Equal(t, "secret", m.options.Network.Cookie,
		"Network cookie should match config secret")
}

func TestReplaceKSUIDs_RewritesEmbeddedSpan(t *testing.T) {
	ksuid := "abc123"
	refEnv := `{"$ref":"formae://` + ksuid + `#/id","$value":"v1"}`
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(refEnv) + "')"

	in, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{"$embed": true, "$template": tmpl},
	})
	require.NoError(t, err)

	out := replaceKSUIDs(string(in), map[string]pkgmodel.TripletKey{
		ksuid: {Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"},
	})

	tmplOut := gjson.Get(out, "functionCode.$template").String()
	spans, err := pkgmodel.ScanEmbedSpans(tmplOut)
	require.NoError(t, err)
	require.Len(t, spans, 1, "expected exactly one span in $template output")

	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, `"$res":true`),
		"span should contain $res:true, got: %s", spans[0].EnvelopeJSON)
	assert.True(t, strings.Contains(spans[0].EnvelopeJSON, `"$label":"kvs"`),
		"span should contain $label:kvs, got: %s", spans[0].EnvelopeJSON)
}

func TestReplaceKSUIDs_RewritesEmbeddedSpan_Idempotent(t *testing.T) {
	ksuid := "abc123"
	refEnv := `{"$ref":"formae://` + ksuid + `#/id","$value":"v1"}`
	tmpl := "cf.kvs('" + pkgmodel.FrameEnvelope(refEnv) + "')"

	in, err := json.Marshal(map[string]any{
		"functionCode": map[string]any{"$embed": true, "$template": tmpl},
	})
	require.NoError(t, err)

	ksuidToTriplet := map[string]pkgmodel.TripletKey{
		ksuid: {Stack: "default", Label: "kvs", Type: "AWS::CloudFront::KeyValueStore"},
	}

	out1 := replaceKSUIDs(string(in), ksuidToTriplet)
	out2 := replaceKSUIDs(out1, ksuidToTriplet)
	assert.Equal(t, out1, out2, "replaceKSUIDs should be idempotent")
}
