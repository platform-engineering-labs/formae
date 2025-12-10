// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build unit

package resolver

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

func TestResolvePropertyReferences(t *testing.T) {
	t.Run("resolves basic reference", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s"
			}
		}`, vpcRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(vpcRef),
			properties,
			`{"VpcId": "vpc-123456"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "vpc-123456", result.Get("VpcId.$value").String())
		assert.Equal(t, vpcRef, result.Get("VpcId.$ref").String())
	})

	t.Run("preserves strategy and visibility", func(t *testing.T) {
		secretRef := newTestRef("Arn")
		properties := fmt.Appendf(nil, `{
			"SecretArn": {
				"$ref": "%s",
				"$strategy": "SetOnce",
				"$visibility": "opaque"
			}
		}`, secretRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(secretRef),
			properties,
			`{"Arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:MySecret"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "arn:aws:secretsmanager:us-east-1:123456789012:secret:MySecret", result.Get("SecretArn.$value").String())
		assert.Equal(t, "SetOnce", result.Get("SecretArn.$strategy").String())
		assert.Equal(t, "opaque", result.Get("SecretArn.$visibility").String())
	})

	t.Run("extracts property from complex resolved value", func(t *testing.T) {
		bucketRef := newTestRef("Arn")
		properties := fmt.Appendf(nil, `{
			"BucketArn": {
				"$ref": "%s"
			}
		}`, bucketRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(bucketRef),
			properties,
			`{"Arn": "arn:aws:s3:::my-bucket", "Name": "my-bucket", "Region": "us-east-1"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "arn:aws:s3:::my-bucket", result.Get("BucketArn.$value").String())
	})

	t.Run("returns error for missing reference", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		nonExistentRef := newTestRef("VpcId") // Different KSUID for same property

		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s"
			}
		}`, vpcRef)

		// Try to resolve with a different KSUID that doesn't exist in properties
		_, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(nonExistentRef),
			properties,
			`{"VpcId": "vpc-123456"}`,
		)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("failed to set property value for %s", nonExistentRef))
	})

	t.Run("resolves only targeted reference among multiple", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		subnetRef := newTestRef("SubnetId")
		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s"
			},
			"SubnetId": {
				"$ref": "%s"
			}
		}`, vpcRef, subnetRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(vpcRef),
			properties,
			`{"VpcId": "vpc-123456"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "vpc-123456", result.Get("VpcId.$value").String())
		assert.False(t, result.Get("SubnetId.$value").Exists())
		assert.Equal(t, subnetRef, result.Get("SubnetId.$ref").String())
	})

	t.Run("handles malformed JSON input", func(t *testing.T) {
		properties := fmt.Appendf(nil, `{"VpcId": {invalid json}`)

		_, err := ResolvePropertyReferences(
			"formae://vpc.ec2.aws/stack/vpc-1#/VpcId",
			properties,
			`{"VpcId": "vpc-123456"}`,
		)

		assert.Error(t, err)
	})

	t.Run("handles null value", func(t *testing.T) {
		properties := json.RawMessage(`{
			"VpcId": {
				"$ref": "formae://vpc.ec2.aws/stack/vpc-1#/VpcId"
			}
		}`)

		resolved, err := ResolvePropertyReferences(
			"formae://vpc.ec2.aws/stack/vpc-1#/VpcId",
			properties,
			`{"VpcId": null}`,
		)

		require.NoError(t, err)

		// null values should be set, but the value should be null
		result := gjson.Parse(string(resolved))
		if result.Get("VpcId.$value").Exists() {
			assert.Nil(t, result.Get("VpcId.$value").Value())
		}

		// the reference should still be preserved
		assert.Equal(t, "formae://vpc.ec2.aws/stack/vpc-1#/VpcId", result.Get("VpcId.$ref").String())
	})

	t.Run("handles boolean values", func(t *testing.T) {
		enabledRef := newTestRef("Enabled")
		properties := fmt.Appendf(nil, `{
			"Enabled": {
				"$ref": "%s"
			}
		}`, enabledRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(enabledRef),
			properties,
			`{"Enabled": true}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.True(t, result.Get("Enabled.$value").Bool())
	})

	t.Run("handles numeric values", func(t *testing.T) {
		portRef := newTestRef("Port")
		properties := fmt.Appendf(nil, `{
			"Port": {
				"$ref": "%s"
			}
		}`, portRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(portRef),
			properties,
			`{"Port": 8080}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, int64(8080), result.Get("Port.$value").Int())
	})

	t.Run("handles deeply nested objects", func(t *testing.T) {
		dbEndpointRef := newTestRef("Endpoint")
		properties := fmt.Appendf(nil, `{
			"Config": {
				"Database": {
					"Host": {
						"$ref": "%s"
					}
				}
			}
		}`, dbEndpointRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(dbEndpointRef),
			properties,
			`{"Endpoint": "db.example.com"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "db.example.com", result.Get("Config.Database.Host.$value").String())
	})

	t.Run("handles partial resolution scenarios", func(t *testing.T) {
		// Test that some references can be resolved while others remain unresolved
		vpcRef := newTestRef("VpcId")
		sgRef := newTestRef("GroupId")
		subnetRef := newTestRef("SubnetId")
		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s",
				"$strategy": "SetOnce"
			},
			"SubnetId": {
				"$ref": "%s"
			},
			"SecurityGroupId": {
				"$ref": "%s"
			}
		}`, vpcRef, subnetRef, sgRef)

		// First, resolve only VpcId
		resolvedOnce, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(vpcRef),
			properties,
			`{"VpcId": "vpc-123456"}`)
		require.NoError(t, err)

		// Then resolve SecurityGroupId on the partially resolved properties
		resolvedTwice, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(sgRef),
			resolvedOnce,
			`{"GroupId": "sg-789012"}`)
		require.NoError(t, err)

		result := gjson.Parse(string(resolvedTwice))

		// VpcId should be resolved
		assert.Equal(t, "vpc-123456", result.Get("VpcId.$value").String())
		assert.Equal(t, "SetOnce", result.Get("VpcId.$strategy").String())

		// SecurityGroupId should be resolved
		assert.Equal(t, "sg-789012", result.Get("SecurityGroupId.$value").String())

		// SubnetId should remain unresolved
		assert.Equal(t, subnetRef, result.Get("SubnetId.$ref").String())
		assert.False(t, result.Get("SubnetId.$value").Exists())
	})

	t.Run("handles complex array references with proper paths", func(t *testing.T) {
		sgHttpRef := newTestRef("GroupId")
		sgHttpsRef := newTestRef("GroupId")
		properties := fmt.Appendf(nil, `{
			"SecurityGroupIngress": [
				{
					"FromPort": 80,
					"IpProtocol": "tcp",
					"SourceSecurityGroupId": {
						"$ref": "%s",
						"$visibility": "Clear"
					},
					"ToPort": 80
				},
				{
					"FromPort": 443,
					"IpProtocol": "tcp",
					"SourceSecurityGroupId": {
						"$ref": "%s",
						"$visibility": "Clear"
					},
					"ToPort": 443
				}
			],
			"VpcId": {
				"$ref": "formae://vpc.ec2.aws/stack/vpc-1#/VpcId"
			}
		}`, sgHttpRef, sgHttpsRef)

		// Resolve one of the array references
		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(sgHttpRef),
			properties,
			`{"GroupId": "sg-http-123"}`)
		require.NoError(t, err)

		result := gjson.Parse(string(resolved))

		// First array element should be resolved
		assert.Equal(t, "sg-http-123", result.Get("SecurityGroupIngress.0.SourceSecurityGroupId.$value").String())
		assert.Equal(t, "Clear", result.Get("SecurityGroupIngress.0.SourceSecurityGroupId.$visibility").String())

		// Second array element should remain unresolved
		assert.Equal(t, sgHttpsRef, result.Get("SecurityGroupIngress.1.SourceSecurityGroupId.$ref").String())
		assert.False(t, result.Get("SecurityGroupIngress.1.SourceSecurityGroupId.$value").Exists())

		// VpcId should remain unresolved
		assert.Equal(t, "formae://vpc.ec2.aws/stack/vpc-1#/VpcId", result.Get("VpcId.$ref").String())
		assert.False(t, result.Get("VpcId.$value").Exists())

		// Static properties should be unchanged
		assert.Equal(t, int64(80), result.Get("SecurityGroupIngress.0.FromPort").Int())
		assert.Equal(t, int64(443), result.Get("SecurityGroupIngress.1.FromPort").Int())
	})
}

func TestConvertToPluginFormat(t *testing.T) {
	t.Run("converts properly resolved references (object usage)", func(t *testing.T) {
		vpcRef := newTestRef("VpcObject")
		properties := fmt.Appendf(nil, `{
			"VpcObject": {
				"$ref": "%s",
				"$value": {"VpcId": "vpc-123456", "State": "available"}
			}
		}`, vpcRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		assert.JSONEq(t, `{"VpcId": "vpc-123456", "State": "available"}`, parsed.Get("VpcObject").String())
	})

	t.Run("handles mixed properly resolved references", func(t *testing.T) {
		vpcRef := newTestRef("VpcObject")
		subnetRef := newTestRef("SubnetObject")
		properties := fmt.Appendf(nil, `{
			"VpcObject": {
				"$ref": "%s",
				"$value": {"VpcId": "vpc-123456", "State": "available"}
			},
			"SubnetObject": {
				"$ref": "%s", 
				"$value": {"SubnetId": "subnet-789012", "AvailabilityZone": "us-east-1a"}
			},
			"BucketName": {
				"$value": "my-bucket"
			},
			"Region": "us-east-1"
		}`, vpcRef, subnetRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))

		vpcGot := parsed.Get("VpcObject").Raw
		subnetGot := parsed.Get("SubnetObject").Raw

		assert.JSONEq(t, `{"VpcId": "vpc-123456", "State": "available"}`, vpcGot)
		assert.JSONEq(t, `{"SubnetId": "subnet-789012", "AvailabilityZone": "us-east-1a"}`, subnetGot)
		assert.Equal(t, "my-bucket", parsed.Get("BucketName").String())
		assert.Equal(t, "us-east-1", parsed.Get("Region").String())
	})

	t.Run("converts resolved references to plain values", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s",
				"$value": "vpc-123456"
			}
		}`, vpcRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		assert.Equal(t, "vpc-123456", parsed.Get("VpcId").String())
	})

	t.Run("converts value objects to plain values", func(t *testing.T) {
		properties := json.RawMessage(`{
			"BucketName": {
				"$value": "my-bucket"
			}
		}`)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		assert.Equal(t, "my-bucket", parsed.Get("BucketName").String())
	})

	t.Run("handles mixed references, values, and static data", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s",
				"$value": "vpc-123456"
			},
			"BucketName": {
				"$value": "my-bucket"
			},
			"Region": "us-east-1",
			"Port": 8080
		}`, vpcRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		assert.Equal(t, "vpc-123456", parsed.Get("VpcId").String())
		assert.Equal(t, "my-bucket", parsed.Get("BucketName").String())
		assert.Equal(t, "us-east-1", parsed.Get("Region").String())
		assert.Equal(t, int64(8080), parsed.Get("Port").Int())
	})

	t.Run("handles arrays with mixed types", func(t *testing.T) {
		sg1Ref := newTestRef("GroupId")
		properties := fmt.Appendf(nil, `{
			"SecurityGroups": [
				{
					"$ref": "%s",
					"$value": "sg-123456"
				},
				{
					"$value": "sg-789012"
				},
				"sg-static"
			]
		}`, sg1Ref)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		sgs := parsed.Get("SecurityGroups").Array()
		assert.Len(t, sgs, 3)
		assert.Equal(t, "sg-123456", sgs[0].String())
		assert.Equal(t, "sg-789012", sgs[1].String())
		assert.Equal(t, "sg-static", sgs[2].String())
	})

	t.Run("preserves unresolved references", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s"
			}
		}`, vpcRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		assert.Equal(t, vpcRef, parsed.Get("VpcId.$ref").String())
		assert.False(t, parsed.Get("VpcId.$value").Exists())
	})

	t.Run("handles various data types", func(t *testing.T) {
		properties := json.RawMessage(`{
			"StringVal": { "$value": "hello" },
			"IntVal": { "$value": 42 },
			"FloatVal": { "$value": 3.14 },
			"BoolVal": { "$value": true },
			"NullVal": { "$value": null },
			"ArrayVal": { "$value": [1, 2, 3] },
			"ObjectVal": { "$value": {"nested": "object"} }
		}`)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		assert.Equal(t, "hello", parsed.Get("StringVal").String())
		assert.Equal(t, int64(42), parsed.Get("IntVal").Int())
		assert.Equal(t, 3.14, parsed.Get("FloatVal").Float())
		assert.True(t, parsed.Get("BoolVal").Bool())
		assert.True(t, parsed.Get("NullVal").Exists())
		assert.Len(t, parsed.Get("ArrayVal").Array(), 3)
		assert.Equal(t, "object", parsed.Get("ObjectVal.nested").String())
	})

	t.Run("handles partially resolved properties", func(t *testing.T) {
		resolvedRef := newTestRef("ResolvedRef")
		unresolvedRef := newTestRef("UnresolvedRef")
		properties := fmt.Appendf(nil, `{
			"ResolvedRef": {
				"$ref": "%s",
				"$value": "vpc-123456"
			},
			"UnresolvedRef": {
				"$ref": "%s"
			},
			"PlainValue": "static-value"
		}`, resolvedRef, unresolvedRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		assert.Equal(t, "vpc-123456", parsed.Get("ResolvedRef").String())
		assert.Equal(t, unresolvedRef, parsed.Get("UnresolvedRef.$ref").String())
		assert.Equal(t, "static-value", parsed.Get("PlainValue").String())
	})

	t.Run("handles complex nested array structures", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		properties := fmt.Appendf(nil, `{
			"ComplexArray": [
				{
					"Name": "item1",
					"VpcId": {
						"$ref": "%s",
						"$value": "vpc-123456"
					}
				},
				{
					"Name": "item2",
					"SubnetIds": [
						{
							"$value": "subnet-123"
						},
						"subnet-static"
					]
				}
			]
		}`, vpcRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		array := parsed.Get("ComplexArray").Array()
		assert.Len(t, array, 2)
		assert.Equal(t, "item1", array[0].Get("Name").String())
		assert.Equal(t, "vpc-123456", array[0].Get("VpcId").String())
		assert.Equal(t, "item2", array[1].Get("Name").String())
		assert.Equal(t, "subnet-123", array[1].Get("SubnetIds.0").String())
		assert.Equal(t, "subnet-static", array[1].Get("SubnetIds.1").String())
	})

	t.Run("converts opaque references to raw values for cloud provider compatibility", func(t *testing.T) {
		secretValueRef := newTestRef("SecretString")
		clearValueRef := newTestRef("VpcId")
		properties := fmt.Appendf(nil, `{
			"SecretValue": {
				"$ref": "%s",
				"$value": "my-secret-password",
				"$visibility": "Opaque"
			},
			"ClearValue": {
				"$ref": "%s",
				"$value": "vpc-123456"
			}
		}`, secretValueRef, clearValueRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))

		assert.Equal(t, "my-secret-password", parsed.Get("SecretValue").String())
		assert.Equal(t, "vpc-123456", parsed.Get("ClearValue").String())

		// Stripped metadata
		assert.False(t, parsed.Get("SecretValue.$visibility").Exists())
	})

	t.Run("handles empty properties gracefully", func(t *testing.T) {
		properties := json.RawMessage(`{}`)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(result))
	})

	t.Run("converts inherited opaque references correctly in plugin format", func(t *testing.T) {
		masterUserPasswordRef := newTestRef("SecretString")
		properties := fmt.Appendf(nil, `{
			"MasterUserPassword": {
				"$ref": "%s",
				"$value": "super-secret-password",
				"$visibility": "Opaque"
			},
			"DatabaseName": {
				"$value": "my-database"
			}
		}`, masterUserPasswordRef)

		result, err := ConvertToPluginFormat(properties)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))

		assert.Equal(t, "super-secret-password", parsed.Get("MasterUserPassword").String())
		assert.Equal(t, "my-database", parsed.Get("DatabaseName").String())

		assert.False(t, parsed.Get("MasterUserPassword.$visibility").Exists())
		assert.False(t, parsed.Get("MasterUserPassword.$ref").Exists())
	})
}

func TestExtractResolvableURIs(t *testing.T) {
	t.Run("extracts basic references", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		subnetRef := newTestRef("SubnetId")
		resource := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"VpcId": {
					"$ref": "%s"
				},
				"SubnetId": {
					"$ref": "%s"
				}
			}`, vpcRef, subnetRef),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Len(t, uris, 2)
		assert.Contains(t, uris, pkgmodel.FormaeURI(vpcRef))
		assert.Contains(t, uris, pkgmodel.FormaeURI(subnetRef))
	})

	t.Run("extracts references from nested objects", func(t *testing.T) {
		dbEndpointRef := newTestRef("Endpoint")
		resource := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"Config": {
					"Database": {
						"Host": {
							"$ref": "%s"
						}
					}
				}
			}`, dbEndpointRef),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Len(t, uris, 1)
		assert.Contains(t, uris, pkgmodel.FormaeURI(dbEndpointRef))
	})

	t.Run("extracts references from arrays", func(t *testing.T) {
		sg1Ref := newTestRef("GroupId")
		sg2Ref := newTestRef("GroupId")
		resource := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"SecurityGroups": [
					{
						"$ref": "%s"
					},
					{
						"$ref": "%s"
					}
				]
			}`, sg1Ref, sg2Ref),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Len(t, uris, 2)
		assert.Contains(t, uris, pkgmodel.FormaeURI(sg1Ref))
		assert.Contains(t, uris, pkgmodel.FormaeURI(sg2Ref))
	})

	t.Run("extracts ALL references including resolved ones", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		resource := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"VpcId": {
					"$ref": "%s",
					"$value": "vpc-123456"
				}
			}`, vpcRef),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Len(t, uris, 1)
		assert.Contains(t, uris, pkgmodel.FormaeURI(vpcRef))
	})

	t.Run("ignores non-reference properties", func(t *testing.T) {
		resource := pkgmodel.Resource{
			Properties: json.RawMessage(`{
				"StaticValue": "hello",
				"ValueObject": {
					"$value": "world"
				},
				"RegularObject": {
					"nested": "data"
				}
			}`),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Empty(t, uris)
	})

	t.Run("returns empty slice for no resolvable URIs", func(t *testing.T) {
		resource := pkgmodel.Resource{
			Properties: json.RawMessage(`{
				"StaticValue": "hello",
				"Number": 42
			}`),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Empty(t, uris)
		assert.NotNil(t, uris) // Should be empty slice, not nil
	})

	t.Run("handles malformed resource properties", func(t *testing.T) {
		resource := pkgmodel.Resource{
			Properties: json.RawMessage(`{invalid json}`),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Empty(t, uris)
	})

	t.Run("extracts all reference types in same resource", func(t *testing.T) {
		unresolvedRef := newTestRef("UnresolvedRef")
		resolvedRef := newTestRef("ResolvedRef")
		resource := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"UnresolvedRef": {
					"$ref": "%s"
				},
				"ResolvedRef": {
					"$ref": "%s",
					"$value": "subnet-123456"
				},
				"ValueOnly": {
					"$value": "some-value"
				},
				"Static": "static-value"
			}`, unresolvedRef, resolvedRef),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Len(t, uris, 2) // both refs, not just unresolved
		assert.Contains(t, uris, pkgmodel.FormaeURI(unresolvedRef))
		assert.Contains(t, uris, pkgmodel.FormaeURI(resolvedRef))
	})

	t.Run("handles deeply nested complex structures", func(t *testing.T) {
		deepRef := newTestRef("DeepProperty")
		resource := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"Level1": {
					"Level2": {
						"Level3": [
							{
								"RefInArray": {
									"$ref": "%s"
								}
							}
						]
					}
				}
			}`, deepRef),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Len(t, uris, 1)
		assert.Contains(t, uris, pkgmodel.FormaeURI(deepRef))
	})

	t.Run("handles empty properties", func(t *testing.T) {
		resource := pkgmodel.Resource{
			Properties: json.RawMessage(`{}`),
		}

		uris := ExtractResolvableURIs(resource)

		assert.Empty(t, uris)
	})

	t.Run("extracts references from complex array structures", func(t *testing.T) {
		// Test that ExtractResolvableURIs finds all references in complex nested arrays
		sgHttpRef := newTestRef("GroupId")
		sgHttpsRef := newTestRef("GroupId")
		subnet1Ref := newTestRef("SubnetId")
		subnet2Ref := newTestRef("SubnetId")
		vpcRef := newTestRef("VpcId")
		resource := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"SecurityGroupIngress": [
					{
						"FromPort": 80,
						"SourceSecurityGroupId": {
							"$ref": "%s",
							"$visibility": "Clear"
						}
					},
					{
						"FromPort": 443,
						"SourceSecurityGroupId": {
							"$ref": "%s",
							"$value": "sg-existing"
						}
					}
				],
				"Subnets": [
					{
						"$ref": "%s"
					},
					{
						"$ref": "%s"
					}
				],
				"VpcId": {
					"$ref": "%s"
				}
			}`, sgHttpRef, sgHttpsRef, subnet1Ref, subnet2Ref, vpcRef),
		}

		uris := ExtractResolvableURIs(resource)

		// Should find all 5 references (2 in SecurityGroupIngress, 2 in Subnets, 1 VpcId)
		assert.Len(t, uris, 5)
		assert.Contains(t, uris, pkgmodel.FormaeURI(sgHttpRef))
		assert.Contains(t, uris, pkgmodel.FormaeURI(sgHttpsRef))
		assert.Contains(t, uris, pkgmodel.FormaeURI(subnet1Ref))
		assert.Contains(t, uris, pkgmodel.FormaeURI(subnet2Ref))
		assert.Contains(t, uris, pkgmodel.FormaeURI(vpcRef))
	})
}

func TestMetadataInheritance(t *testing.T) {
	t.Run("inherits metadata from referenced property", func(t *testing.T) {
		secretRef := newTestRef("SecretString")
		properties := json.RawMessage(fmt.Sprintf(`{
			"MasterUserPassword": {
				"$ref": "%s"
			}
		}`, secretRef))

		propValue := `{"$value": "super-secret-password", "$visibility": "Opaque", "$strategy": "SetOnce"}`

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(secretRef),
			properties,
			propValue,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))

		assert.Equal(t, "super-secret-password", result.Get("MasterUserPassword.$value").String())
		assert.Equal(t, "Opaque", result.Get("MasterUserPassword.$visibility").String())
		assert.Equal(t, "SetOnce", result.Get("MasterUserPassword.$strategy").String())
		assert.Equal(t, secretRef, result.Get("MasterUserPassword.$ref").String())
	})

	t.Run("handles simple string values without metadata", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		properties := fmt.Appendf(nil, `{
			"VpcId": {
				"$ref": "%s"
			}
		}`, vpcRef)

		// no metadata
		simpleValue := "vpc-123456"

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(vpcRef),
			properties,
			simpleValue,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))

		assert.Equal(t, "vpc-123456", result.Get("VpcId.$value").String())
		assert.False(t, result.Get("VpcId.$visibility").Exists())
		assert.False(t, result.Get("VpcId.$strategy").Exists())
	})

	t.Run("inherits only available metadata fields", func(t *testing.T) {
		secretRef := newTestRef("SecretString")
		properties := fmt.Appendf(nil, `{
			"DatabasePassword": {
				"$ref": "%s"
			}
		}`, secretRef)

		partialMetadata := `{"$value": "my-password", "$visibility": "Opaque"}`

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(secretRef),
			properties,
			partialMetadata,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))

		assert.Equal(t, "my-password", result.Get("DatabasePassword.$value").String())
		assert.Equal(t, "Opaque", result.Get("DatabasePassword.$visibility").String())

		// Should not set strategy when not provided
		assert.False(t, result.Get("DatabasePassword.$strategy").Exists())
	})
}

func TestResolveMultiplePropertiesSameResolverInstance(t *testing.T) {
	restApiIdRef := newTestRef("RestApiId")
	parentIdRef := newTestRef("RootResourceId")
	properties := fmt.Appendf(nil, `{
		"RestApiId": {
			"$ref": "%s",
			"$visibility": "Clear"
		},
		"ParentId": {
			"$ref": "%s",
			"$visibility": "Clear"
		}
	}`, restApiIdRef, parentIdRef)
	resolver := newPropertyResolver(properties)

	err := resolver.setRefValue(
		pkgmodel.FormaeURI(restApiIdRef),
		`{"RestApiId": "fhu1gfgwhc", "RootResourceId": "7c5bfje8c3"}`,
	)
	require.NoError(t, err)

	err = resolver.setRefValue(
		pkgmodel.FormaeURI(parentIdRef),
		`{"RestApiId": "fhu1gfgwhc", "RootResourceId": "7c5bfje8c3"}`,
	)
	require.NoError(t, err)

	resolved, err := resolver.resolveReferences(properties)
	require.NoError(t, err)

	result := gjson.Parse(string(resolved))

	restApiValue := result.Get("RestApiId.$value").String()
	parentIdValue := result.Get("ParentId.$value").String()

	assert.Equal(t, "fhu1gfgwhc", restApiValue)
	assert.Equal(t, "7c5bfje8c3", parentIdValue)
}

func TestSameURIMultiplePaths(t *testing.T) {
	t.Run("resolves same URI at multiple array paths", func(t *testing.T) {
		gwRef := newTestRef("GatewayId")
		props := fmt.Appendf(nil, `{
			"RouteRules": [
				{
					"Destination": "10.0.0.0/8",
					"NetworkEntityId": { "$ref": "%s" }
				},
				{
					"Destination": "172.16.0.0/12",
					"NetworkEntityId": { "$ref": "%s" }
				}
			]
		}`, gwRef, gwRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(gwRef),
			props,
			`{"GatewayId": "gw-123456"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "gw-123456", result.Get("RouteRules.0.NetworkEntityId.$value").String())
		assert.Equal(t, "gw-123456", result.Get("RouteRules.1.NetworkEntityId.$value").String())
	})

	t.Run("resolves same URI at multiple top-level paths", func(t *testing.T) {
		vpcRef := newTestRef("VpcId")
		props := fmt.Appendf(nil, `{
			"SourceVpcId": { "$ref": "%s" },
			"DestinationVpcId": { "$ref": "%s" }
		}`, vpcRef, vpcRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(vpcRef),
			props,
			`{"VpcId": "vpc-123456"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "vpc-123456", result.Get("SourceVpcId.$value").String())
		assert.Equal(t, "vpc-123456", result.Get("DestinationVpcId.$value").String())
	})

	t.Run("returns unique URIs when same ref appears multiple times", func(t *testing.T) {
		sharedRef := newTestRef("SharedId")
		res := pkgmodel.Resource{
			Properties: fmt.Appendf(nil, `{
				"Field1": { "$ref": "%s" },
				"Field2": { "$ref": "%s" },
				"Field3": { "$ref": "%s" }
			}`, sharedRef, sharedRef, sharedRef),
		}

		uris := ExtractResolvableURIs(res)

		assert.Len(t, uris, 1)
		assert.Contains(t, uris, pkgmodel.FormaeURI(sharedRef))
	})

	t.Run("converts all paths when same URI resolved at multiple locations", func(t *testing.T) {
		sgRef := newTestRef("SecurityGroupId")
		props := fmt.Appendf(nil, `{
			"SecurityGroupIds": [
				{ "$ref": "%s", "$value": "sg-123" },
				{ "$ref": "%s", "$value": "sg-123" }
			]
		}`, sgRef, sgRef)

		result, err := ConvertToPluginFormat(props)

		require.NoError(t, err)

		parsed := gjson.Parse(string(result))
		sgs := parsed.Get("SecurityGroupIds").Array()
		assert.Len(t, sgs, 2)
		assert.Equal(t, "sg-123", sgs[0].String())
		assert.Equal(t, "sg-123", sgs[1].String())
	})

	t.Run("resolves same URI in deeply nested structures", func(t *testing.T) {
		subnetRef := newTestRef("SubnetId")
		props := fmt.Appendf(nil, `{
			"Config": {
				"Primary": { "$ref": "%s" },
				"Failover": { "$ref": "%s" }
			}
		}`, subnetRef, subnetRef)

		resolved, err := ResolvePropertyReferences(
			pkgmodel.FormaeURI(subnetRef),
			props,
			`{"SubnetId": "subnet-abc"}`,
		)

		require.NoError(t, err)

		result := gjson.Parse(string(resolved))
		assert.Equal(t, "subnet-abc", result.Get("Config.Primary.$value").String())
		assert.Equal(t, "subnet-abc", result.Get("Config.Failover.$value").String())
	})
}

// newTestRef creates a test reference with a real KSUID for the given property
func newTestRef(property string) string {
	ksuid := util.NewID()
	return fmt.Sprintf("formae://%s#/%s", ksuid, property)
}
