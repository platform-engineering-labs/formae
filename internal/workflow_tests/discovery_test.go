// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package workflow_tests

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-engineering-labs/formae/internal/metastructure/discovery"
	"github.com/platform-engineering-labs/formae/internal/metastructure/resource_update"
	"github.com/platform-engineering-labs/formae/internal/metastructure/testutil"
	"github.com/platform-engineering-labs/formae/internal/metastructure/util"
	"github.com/platform-engineering-labs/formae/internal/workflow_tests/test_helpers"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

func TestDiscovery_FindsAndCreatesNewResources(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			List: func(request *resource.ListRequest) (*resource.ListResult, error) {
				// Since we only return buckets from read, we must gate list requests in the same way
				if request.ResourceType != "FakeAWS::S3::Bucket" {
					return &resource.ListResult{Resources: nil, NextPageToken: nil}, nil
				}
				if awsRegionFromTarget(t, request.Target) == "us-east-1" {
					if request.PageToken == nil {
						return &resource.ListResult{
							Resources: []resource.Resource{
								{
									NativeID:   "test-resource-1",
									Properties: `{"Tags": {"Name": "test-resource-1", "Environment": "test"}}`,
								},
								{
									NativeID:   "test-resource-2",
									Properties: `{"Tags": {"Name": "test-resource-2", "Environment": "test"}, "foo": "bar"}`,
								},
							},
							NextPageToken: util.StringPtr("abcdef"),
						}, nil
					} else if *request.PageToken == "abcdef" {
						return &resource.ListResult{
							Resources: []resource.Resource{
								{
									NativeID:   "test-resource-3",
									Properties: `{"Tags": {"Name": "test-resource-3", "Environment": "test"}, "baz": "qux"}`,
								},
							},
							NextPageToken: nil, // No more pages
						}, nil
					}
					return nil, fmt.Errorf("unexpected page token: %v", request.PageToken)
				} else if awsRegionFromTarget(t, request.Target) == "us-west-2" {
					if request.PageToken == nil {
						return &resource.ListResult{
							Resources: []resource.Resource{
								{
									NativeID:   "test-resource-4",
									Properties: `{"Tags": {"Name": "test-resource-4", "Environment": "test"}, "bar": "baz"}`,
								},
							},
							NextPageToken: nil, // No more pages
						}, nil
					}
					return nil, fmt.Errorf("unexpected page token: %v", request.PageToken)
				}
				return nil, fmt.Errorf("unexpected region: %v", awsRegionFromTarget(t, request.Target))
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				if awsRegionFromTarget(t, request.Target) == "us-east-1" {
					switch request.NativeID {
					case "test-resource-2":
						return &resource.ReadResult{
							ResourceType: "FakeAWS::S3::Bucket",
							Properties:   fmt.Sprintf(`{"Tags": {"Name": "%s", "Environment": "test"}, "foo": "bar"}`, request.NativeID),
						}, nil
					case "test-resource-3":
						return &resource.ReadResult{
							ResourceType: "FakeAWS::S3::Bucket",
							Properties:   fmt.Sprintf(`{"Tags": {"Name": "%s", "Environment": "test"}, "baz": "qux"}`, request.NativeID),
						}, nil
					}
				} else if awsRegionFromTarget(t, request.Target) == "us-west-2" {
					return &resource.ReadResult{
						ResourceType: "FakeAWS::S3::Bucket",
						Properties:   fmt.Sprintf(`{"Tags": {"Name": "%s", "Environment": "test"}, "bar": "baz"}`, request.NativeID),
					}, nil
				}
				return nil, fmt.Errorf("unexpected region: %v", awsRegionFromTarget(t, request.Target))
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		if err != nil {
			t.Fatalf("Failed to create metastructure: %v", err)
			return
		}

		target1 := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target1)
		assert.NoError(t, err)

		target2 := &pkgmodel.Target{
			Label:        "us-west-2",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-west-2"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target2)
		assert.NoError(t, err)

		// start test helper actor to interact with the actors in the metastructure
		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		assert.NoError(t, err)

		// store resource-1 to test de-duplication
		resource1 := resourceUpdateCreatingResource1()
		hash, err := testutil.Call(m.Node, "ResourcePersister", resource_update.PersistResourceUpdate{
			PluginOperation: resource.OperationCreate,
			ResourceUpdate:  *resource1,
		})
		assert.NoError(t, err)
		assert.NotEmpty(t, hash)

		// send discovery request
		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		assert.NoError(t, err)

		var stack *pkgmodel.Forma
		// assert that the resources persisted in the datastore
		require.Eventually(t, func() bool {
			stack, err = m.Datastore.LoadStack("$unmanaged")
			if err != nil {
				t.Logf("Error loading stack: %v", err)
				return false
			}
			return stack != nil && len(stack.Resources) == 3
		},
			5*time.Second,
			100*time.Millisecond)

		resource2 := findResourceByNativeID("test-resource-2", stack)
		assert.NotNil(t, resource2)
		assert.False(t, resource2.Managed)

		resource3 := findResourceByNativeID("test-resource-3", stack)
		assert.NotNil(t, resource3)
		assert.False(t, resource3.Managed)

		resource4 := findResourceByNativeID("test-resource-4", stack)
		assert.NotNil(t, resource4)
		assert.False(t, resource4.Managed)
	})
}

func findResourceByNativeID(nativeID string, stack *pkgmodel.Forma) *pkgmodel.Resource {
	for i := range stack.Resources {
		if stack.Resources[i].NativeID == nativeID {
			return &stack.Resources[i]
		}
	}
	return nil
}

func TestDiscovery_DiscoversNestedResources(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		// test nested resources both from managed and unmanaged parents
		overrides := &plugin.ResourcePluginOverrides{
			List: func(request *resource.ListRequest) (*resource.ListResult, error) {
				switch request.ResourceType {
				case "FakeAWS::EC2::VPC":
					return &resource.ListResult{
						Resources: []resource.Resource{
							{
								NativeID:   "vpc-2",
								Properties: `{"VpcId":"vpc-2"}`,
							},
						},
					}, nil
				case "FakeAWS::EC2::VPCCidrBlock":
					switch request.AdditionalProperties["VpcId"] {
					case "vpc-1":
						return &resource.ListResult{
							ResourceType: "FakeAWS::EC2::VPCCidrBlock",
							Resources: []resource.Resource{
								{
									NativeID:   "vpc-1-cidr-1",
									Properties: `{}`,
								},
							}}, nil
					case "vpc-2":
						return &resource.ListResult{
							ResourceType: "FakeAWS::EC2::VPCCidrBlock",
							Resources: []resource.Resource{
								{
									NativeID:   "vpc-2-cidr-1",
									Properties: `{}`,
								},
							}}, nil
					default:
						return nil, fmt.Errorf("unexpected VpcId: %v", request.AdditionalProperties["VpcId"])
					}
				default:
					return &resource.ListResult{Resources: nil, NextPageToken: nil}, nil
				}
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				switch request.NativeID {
				case "vpc-2":
					return &resource.ReadResult{
						ResourceType: "FakeAWS::EC2::VPC",
						Properties:   `{"VpcId":"vpc-2"}`,
					}, nil
				case "vpc-1-cidr-1":
					return &resource.ReadResult{
						ResourceType: "FakeAWS::EC2::VPCCidrBlock",
						Properties:   `{"VpcId":"vpc-1","CidrBlock":"10.0.1.0/16"}`,
					}, nil
				case "vpc-2-cidr-1":
					return &resource.ReadResult{
						ResourceType: "FakeAWS::EC2::VPCCidrBlock",
						Properties:   `{"VpcId":"vpc-2","CidrBlock":"10.0.1.0/24"}`,
					}, nil
				default:
					return nil, fmt.Errorf("unexpected native id: %s", request.NativeID)
				}
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		target := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target)
		require.NoError(t, err)

		_, err = m.Datastore.StoreResource(&pkgmodel.Resource{
			Label:      "vpc-1",
			Type:       "FakeAWS::EC2::VPC",
			NativeID:   "vpc-1",
			Stack:      "infrastructure",
			Target:     "us-east-1",
			Properties: json.RawMessage(`{"VpcId":"vpc-1"}`),
		}, "test-nested-resources-id")
		require.NoError(t, err)

		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		require.NoError(t, err)

		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		require.NoError(t, err)

		var unmanagedStack *pkgmodel.Forma
		require.Eventually(t, func() bool {
			unmanagedStack, err = m.Datastore.LoadStack("$unmanaged")
			if err != nil || unmanagedStack == nil {
				return false
			}
			return len(unmanagedStack.Resources) == 3
		}, 10*time.Second, 100*time.Millisecond)

		idxVpc2 := slices.IndexFunc(unmanagedStack.Resources, func(r pkgmodel.Resource) bool {
			return r.NativeID == "vpc-2"
		})
		require.NotEqual(t, -1, idxVpc2)
		vpc2 := unmanagedStack.Resources[idxVpc2]

		idxCidr2 := slices.IndexFunc(unmanagedStack.Resources, func(r pkgmodel.Resource) bool {
			return r.NativeID == "vpc-2-cidr-1"
		})
		require.NotEqual(t, -1, idxCidr2)
		vpcCidrBlock2 := unmanagedStack.Resources[idxCidr2]

		assert.JSONEq(t, fmt.Sprintf(`{
						"VpcId": {
							"$ref": "formae://%s#/VpcId",
							"$value": "vpc-2"
						},
						"CidrBlock": "10.0.1.0/24"
					}`, vpc2.Ksuid), string(vpcCidrBlock2.Properties))

		infraStack, err := m.Datastore.LoadStack("infrastructure")
		require.NoError(t, err)
		require.Len(t, infraStack.Resources, 1)

		vpc1 := infraStack.Resources[0]

		idxCidr1 := slices.IndexFunc(unmanagedStack.Resources, func(r pkgmodel.Resource) bool {
			return r.NativeID == "vpc-1-cidr-1"
		})
		assert.NotEqual(t, -1, idxCidr1)
		vpcCidrBlock1 := unmanagedStack.Resources[idxCidr1]

		assert.JSONEq(t, fmt.Sprintf(`{
						"VpcId": {
							"$ref": "formae://%s#/VpcId",
							"$value": "vpc-1"
						},
						"CidrBlock": "10.0.1.0/16"
					}`, vpc1.Ksuid), string(vpcCidrBlock1.Properties))
	})
}

func TestDiscovery_DiscoversNestedResourcesWhenAllParentsAlreadyExist(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			List: func(request *resource.ListRequest) (*resource.ListResult, error) {
				switch request.ResourceType {
				case "FakeAWS::EC2::VPC":
					// Return two VPCs that both already exist in the database
					return &resource.ListResult{
						Resources: []resource.Resource{
							{
								NativeID:   "vpc-1",
								Properties: `{"VpcId":"vpc-1"}`,
							},
							{
								NativeID:   "vpc-2",
								Properties: `{"VpcId":"vpc-2"}`,
							},
						},
					}, nil
				case "FakeAWS::EC2::VPCCidrBlock":
					// Return nested resources for both VPCs
					switch request.AdditionalProperties["VpcId"] {
					case "vpc-1":
						return &resource.ListResult{
							Resources: []resource.Resource{
								{
									NativeID:   "vpc-1-cidr-1",
									Properties: `{}`,
								},
							}}, nil
					case "vpc-2":
						return &resource.ListResult{
							Resources: []resource.Resource{
								{
									NativeID:   "vpc-2-cidr-1",
									Properties: `{}`,
								},
							}}, nil
					default:
						return nil, fmt.Errorf("unexpected VpcId: %v", request.AdditionalProperties["VpcId"])
					}
				default:
					return &resource.ListResult{Resources: nil, NextPageToken: nil}, nil
				}
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				switch request.NativeID {
				case "vpc-1":
					return &resource.ReadResult{
						ResourceType: "FakeAWS::EC2::VPC",
						Properties:   `{"VpcId":"vpc-1"}`,
					}, nil
				case "vpc-2":
					return &resource.ReadResult{
						ResourceType: "FakeAWS::EC2::VPC",
						Properties:   `{"VpcId":"vpc-2"}`,
					}, nil
				case "vpc-1-cidr-1":
					return &resource.ReadResult{
						ResourceType: "FakeAWS::EC2::VPCCidrBlock",
						Properties:   `{"VpcId":"vpc-1","CidrBlock":"10.0.1.0/16"}`,
					}, nil
				case "vpc-2-cidr-1":
					return &resource.ReadResult{
						ResourceType: "FakeAWS::EC2::VPCCidrBlock",
						Properties:   `{"VpcId":"vpc-2","CidrBlock":"10.0.1.0/24"}`,
					}, nil
				default:
					return nil, fmt.Errorf("unexpected native id: %s", request.NativeID)
				}
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		target := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target)
		require.NoError(t, err)

		// Preload VPCs in the db. Both will be dedupe'd on discovery
		_, err = m.Datastore.StoreResource(&pkgmodel.Resource{
			Label:      "vpc-1",
			Type:       "FakeAWS::EC2::VPC",
			NativeID:   "vpc-1",
			Stack:      "infrastructure",
			Target:     "us-east-1",
			Properties: json.RawMessage(`{"VpcId":"vpc-1"}`),
		}, "test-bug2-id-1")
		require.NoError(t, err)

		_, err = m.Datastore.StoreResource(&pkgmodel.Resource{
			Label:      "vpc-2",
			Type:       "FakeAWS::EC2::VPC",
			NativeID:   "vpc-2",
			Stack:      "infrastructure",
			Target:     "us-east-1",
			Properties: json.RawMessage(`{"VpcId":"vpc-2"}`),
		}, "test-bug2-id-2")
		require.NoError(t, err)

		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		require.NoError(t, err)

		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		require.NoError(t, err)

		var stack *pkgmodel.Forma
		assert.Eventually(t, func() bool {
			stack, err = m.Datastore.LoadStack("$unmanaged")
			if err != nil || stack == nil {
				return false
			}
			return len(stack.Resources) == 2
		}, 10*time.Second, 100*time.Millisecond, "Expected 2 CIDR blocks to be discovered even though all parents already existed")

		require.NotNil(t, stack)
		cidrBlockNativeIDs := make([]string, 0, len(stack.Resources))
		for _, res := range stack.Resources {
			assert.Equal(t, "FakeAWS::EC2::VPCCidrBlock", res.Type)
			cidrBlockNativeIDs = append(cidrBlockNativeIDs, res.NativeID)
		}
		assert.Contains(t, cidrBlockNativeIDs, "vpc-1-cidr-1")
		assert.Contains(t, cidrBlockNativeIDs, "vpc-2-cidr-1")
	})
}

func TestDiscovery_OverlapProtection(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		blockFirstDiscovery := make(chan struct{})
		firstDiscoveryStarted := make(chan struct{})

		overrides := &plugin.ResourcePluginOverrides{
			List: func(request *resource.ListRequest) (*resource.ListResult, error) {
				// Since we only return buckets from read, we must gate list requests in the same way
				if request.ResourceType != "FakeAWS::S3::Bucket" {
					return &resource.ListResult{Resources: nil, NextPageToken: nil}, nil
				}
				select {
				case firstDiscoveryStarted <- struct{}{}:
					t.Logf("Signaled first discovery started")
				default:
					// Do nothing. We've already started the first discovery and don't care
				}

				<-blockFirstDiscovery

				return &resource.ListResult{
					Resources: []resource.Resource{
						{
							NativeID:   "overlap-test-resource",
							Properties: `{"Tags": {"Name": "overlap-test-resource"}}`,
						},
					},
					NextPageToken: nil,
				}, nil
			},
			Read: func(request *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   fmt.Sprintf(`{"Tags": {"Name": "%s"}}`, request.NativeID),
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		target1 := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target1)
		require.NoError(t, err)

		target2 := &pkgmodel.Target{
			Label:        "us-west-2",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-west-2"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target2)
		require.NoError(t, err)

		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		assert.NoError(t, err)

		t.Logf("Sending first discover message")
		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		assert.NoError(t, err)

		t.Logf("Waiting for first discovery to start")
		<-firstDiscoveryStarted

		t.Logf("Sending second discover message")
		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		assert.NoError(t, err)

		t.Logf("Unblocking first discovery")
		close(blockFirstDiscovery)

		assert.Eventually(t, func() bool {
			stack, loadStackErr := m.Datastore.LoadStack("$unmanaged")
			if loadStackErr != nil {
				t.Logf("Error loading stack: %v", loadStackErr)
				return false
			}
			return stack != nil && len(stack.Resources) == 1
		}, 5*time.Second, 100*time.Millisecond, "Should have exactly one resource despite overlap")

		stack, err := m.Datastore.LoadStack("$unmanaged")
		assert.NoError(t, err)
		assert.NotNil(t, stack)
		assert.Equal(t, "overlap-test-resource", stack.Resources[0].NativeID)
		assert.False(t, stack.Resources[0].Managed)
	})
}

func TestDiscovery_NoTagKeysAreFound_LabelIsSetToNativeId(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			List: func(req *resource.ListRequest) (*resource.ListResult, error) {
				switch req.ResourceType {
				case "FakeAWS::S3::Bucket":
					// Return same resources for both regions (simulating global S3 buckets)
					region := awsRegionFromTarget(t, req.Target)
					if region == "us-east-1" || region == "us-west-2" {
						return &resource.ListResult{
							Resources: []resource.Resource{
								{
									NativeID:   "bucket-with-name",
									Properties: `{}`,
								},
								{
									NativeID:   "bucket-without-name",
									Properties: `{}`,
								},
							},
						}, nil
					}
					return nil, fmt.Errorf("unexpected region: %v", region)
				default:
					return nil, fmt.Errorf("unexpected resource type: %v", req.ResourceType)
				}
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				if req.NativeID == "bucket-with-name" {
					return &resource.ReadResult{
						ResourceType: "FakeAWS::S3::Bucket",
						Properties: `{
						"Tags": [
						{"Key":"Environment","Value":"test"},
						{"Key":"Name","Value":"pretty-bucket"}
						]
						}`,
					}, nil
				}
				if req.NativeID == "bucket-without-name" {
					return &resource.ReadResult{
						ResourceType: "FakeAWS::S3::Bucket",
						Properties: `{
						"Tags": [
						{"Key":"Environment","Value":"test"}
						]
						}`,
					}, nil
				}
				return nil, fmt.Errorf("unexpected native id: %s", req.NativeID)
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		cfg.Agent.Discovery.LabelTagKeys = []string{"Name"}

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		target1 := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target1)
		require.NoError(t, err)

		target2 := &pkgmodel.Target{
			Label:        "us-west-2",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-west-2"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target2)
		require.NoError(t, err)

		incoming := make(chan any, 1)
		_, err = testutil.StartTestHelperActor(m.Node, incoming)
		require.NoError(t, err)

		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			stack, err := m.Datastore.LoadStack("$unmanaged")
			if err != nil || stack == nil {
				return false
			}
			if len(stack.Resources) != 2 {
				return false
			}
			// map by native id for assertions
			var withName, withoutName *pkgmodel.Resource
			for i := range stack.Resources {
				r := &stack.Resources[i]
				switch r.NativeID {
				case "bucket-with-name":
					withName = r
				case "bucket-without-name":
					withoutName = r
				}
			}
			return withName != nil && withName.Label == "pretty-bucket" &&
				withoutName != nil && withoutName.Label == "bucket-without-name"
		}, 10*time.Second, 100*time.Millisecond)
	})
}

func TestDiscovery_DiscoveryReadSetsRedactSensitiveIntent(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		var detected bool
		overrides := &plugin.ResourcePluginOverrides{
			List: func(req *resource.ListRequest) (*resource.ListResult, error) {
				if req.ResourceType != "FakeAWS::S3::Bucket" {
					return &resource.ListResult{}, nil
				}
				return &resource.ListResult{Resources: []resource.Resource{{NativeID: "bucket-1", Properties: `{}`}}}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				detected = req.RedactSensitive
				return &resource.ReadResult{ResourceType: "FakeAWS::S3::Bucket", Properties: `{}`}, nil
			},
		}
		cfg := test_helpers.NewTestMetastructureConfig()

		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		target := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target)
		require.NoError(t, err)
		_, err = testutil.StartTestHelperActor(m.Node, make(chan any, 1))
		require.NoError(t, err)

		require.NoError(t, testutil.Send(m.Node, "Discovery", discovery.Discover{}))
		assert.Eventually(t, func() bool { return detected }, 5*time.Second, 100*time.Millisecond, "RedactSensitive should be true during discovery reads")
	})
}

func awsRegionFromTarget(t *testing.T, target *pkgmodel.Target) string {
	var config map[string]string
	err := json.Unmarshal(target.Config, &config)
	assert.NoError(t, err, "Failed to unmarshal target properties")

	return config["region"]
}

func resourceUpdateCreatingResource1() *resource_update.ResourceUpdate {
	return &resource_update.ResourceUpdate{
		Resource: pkgmodel.Resource{
			Label: "test-resource",
			Type:  "FakeAWS::S3::Bucket",
			Schema: pkgmodel.Schema{
				Fields: []string{"foo", "baz", "a"},
			},
			Properties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
			Stack:      "test-stack",
		},
		ResourceTarget: pkgmodel.Target{
			Label:     "us-east-1",
			Namespace: "FakeAWS",
			Config:    json.RawMessage(`{"region": "us-east-1"}`),
		},
		Operation: resource_update.OperationCreate,
		State:     resource_update.ResourceUpdateStateSuccess,
		ProgressResult: []resource.ProgressResult{
			{
				Operation:          resource.OperationCreate,
				OperationStatus:    resource.OperationStatusSuccess,
				RequestID:          "test-request-id",
				NativeID:           "test-resource-1",
				ResourceType:       "FakeAWS::S3::Bucket",
				ResourceProperties: json.RawMessage(`{"foo":"bar","baz":"qux","a":[3,4,2]}`),
				StartTs:            util.TimeNow(),
				ModifiedTs:         util.TimeNow(),
				Attempts:           1,
				MaxAttempts:        3,
			},
		},
		RemainingResolvables: []pkgmodel.FormaeURI{},
		StackLabel:           "test-stack",
		GroupID:              "test-group-id",
	}
}

func TestDiscovery_NoDiscoverableTargets_CompletesImmediately(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		cfg := test_helpers.NewTestMetastructureConfig()
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, nil, cfg)
		defer def()
		require.NoError(t, err)

		target := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: false,
		}
		_, err = m.Datastore.CreateTarget(target)
		require.NoError(t, err)

		_, err = testutil.StartTestHelperActor(m.Node, make(chan any, 1))
		require.NoError(t, err)

		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		require.NoError(t, err)

		time.Sleep(time.Millisecond * 500)
		stack, err := m.Datastore.LoadStack("$unmanaged")
		require.NoError(t, err)
		assert.Nil(t, stack, "No unmanaged stack should be created when no discoverable targets exist")
	})
}

func TestDiscovery_ListPropertiesNotPersistedOnlyReadProperties(t *testing.T) {
	testutil.RunTestFromProjectRoot(t, func(t *testing.T) {
		overrides := &plugin.ResourcePluginOverrides{
			List: func(req *resource.ListRequest) (*resource.ListResult, error) {
				if req.ResourceType != "FakeAWS::S3::Bucket" {
					return &resource.ListResult{}, nil
				}
				return &resource.ListResult{
					Resources: []resource.Resource{
						{
							NativeID:   "test-bucket",
							Properties: `{"ListOnlyProp": "should-not-persist", "BucketName": "list-value"}`,
						},
					},
				}, nil
			},
			Read: func(req *resource.ReadRequest) (*resource.ReadResult, error) {
				return &resource.ReadResult{
					ResourceType: "FakeAWS::S3::Bucket",
					Properties:   `{"BucketName": "read-value", "ReadOnlyProp": "from-read"}`,
				}, nil
			},
		}

		cfg := test_helpers.NewTestMetastructureConfig()
		m, def, err := test_helpers.NewTestMetastructureWithConfig(t, overrides, cfg)
		defer def()
		require.NoError(t, err)

		target := &pkgmodel.Target{
			Label:        "us-east-1",
			Namespace:    "FakeAWS",
			Config:       json.RawMessage(`{"region":"us-east-1"}`),
			Discoverable: true,
		}
		_, err = m.Datastore.CreateTarget(target)
		require.NoError(t, err)

		_, err = testutil.StartTestHelperActor(m.Node, make(chan any, 1))
		require.NoError(t, err)

		err = testutil.Send(m.Node, "Discovery", discovery.Discover{})
		require.NoError(t, err)

		var stack *pkgmodel.Forma
		require.Eventually(t, func() bool {
			stack, err = m.Datastore.LoadStack("$unmanaged")
			return err == nil && stack != nil && len(stack.Resources) == 1
		}, 5*time.Second, 100*time.Millisecond)

		res := stack.Resources[0]
		var props map[string]any
		require.NoError(t, json.Unmarshal(res.Properties, &props))

		assert.Equal(t, "read-value", props["BucketName"], "BucketName should come from READ, not LIST")
		assert.NotContains(t, props, "ListOnlyProp", "LIST-only properties should not be persisted")
	})
}
