// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

//go:build e2e

package route53

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/google/uuid"
	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/ccx"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
	"github.com/stretchr/testify/assert"
)

func createTestHostedZone(zoneName string) (string, error) {
	cfg := &config.Config{}

	client, err := ccx.NewClient(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create client: %w", err)
	}

	props := map[string]any{
		"Name": zoneName,
	}
	propsBytes, _ := json.Marshal(props)

	createReq := &resource.CreateRequest{
		Resource: &model.Resource{
			Type:       "AWS::Route53::HostedZone",
			Properties: propsBytes,
		},
	}

	createRes, err := client.CreateResource(context.Background(), createReq)
	if err != nil {
		return "", fmt.Errorf("create failed: %w", err)
	}
	if createRes == nil || createRes.ProgressResult == nil || createRes.ProgressResult.RequestID == "" {
		return "", fmt.Errorf("create did not return a valid request ID")
	}

	for i := 0; i < 20; i++ {
		statusRes, err := client.StatusResource(context.Background(), &resource.StatusRequest{
			RequestID: createRes.ProgressResult.RequestID,
		}, func(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
			return client.ReadResource(ctx, req)
		})
		if err != nil {
			return "", fmt.Errorf("status failed: %w", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			return statusRes.ProgressResult.NativeID, nil
		}
		time.Sleep(3 * time.Second)
	}
	return "", fmt.Errorf("timeout waiting for hosted zone creation")
}

func deleteTestHostedZone(t *testing.T, hostedZoneID string) {
	cfg := &config.Config{}

	client, err := ccx.NewClient(cfg)
	assert.NoError(t, err)

	// Build metadata for deletion
	props := map[string]any{
		"HostedZoneId": hostedZoneID,
	}
	propsBytes, _ := json.Marshal(props)
	metadata := string(propsBytes)

	deleteReq := &resource.DeleteRequest{
		NativeID:     &hostedZoneID,
		ResourceType: "AWS::Route53::HostedZone",
		Metadata:     json.RawMessage(metadata),
	}

	deleteRes, err := client.DeleteResource(context.Background(), deleteReq)
	assert.NoError(t, err)

	if deleteRes == nil || deleteRes.ProgressResult == nil || deleteRes.ProgressResult.RequestID == "" {
		t.Fatalf("Delete did not return a valid RequestID")
	}

	// Wait for deletion to complete
	assert.Eventually(t, func() bool {
		statusRes, err := client.StatusResource(context.Background(), &resource.StatusRequest{
			RequestID: deleteRes.ProgressResult.RequestID,
		}, func(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
			return client.ReadResource(ctx, req)
		})
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		return statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess
	}, 2*time.Minute, 5*time.Second, "Timed out waiting for hosted zone deletion")
}

func create_record_set(rs RecordSet, propsBytes json.RawMessage, t *testing.T) resource.CreateResult {
	createRes, err := rs.Create(context.Background(), &resource.CreateRequest{
		Resource: &model.Resource{
			Label:      "pel-record-resolvable",
			Type:       "AWS::Route53::RecordSet",
			Stack:      "pel-dns",
			Properties: propsBytes,
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if createRes == nil || createRes.ProgressResult == nil || createRes.ProgressResult.RequestID == "" {
		t.Fatalf("Create did not return a valid RequestID")
	}
	return *createRes
}

func wait_for_status(rs RecordSet, requestID string, metadata string, t *testing.T) *resource.StatusResult {
	for {
		statusRes, err := rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    requestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     json.RawMessage(metadata),
		})
		if err != nil {
			t.Fatalf("Status (create) failed: %v", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			return statusRes
		}
		time.Sleep(2 * time.Second)
	}
}

func delete_record_set(rs RecordSet, nativeID string, metadata string, t *testing.T) *resource.DeleteResult {
	deleteReq := &resource.DeleteRequest{
		NativeID:     &nativeID,
		ResourceType: "AWS::Route53::RecordSet",
		Metadata:     json.RawMessage(metadata),
	}
	deleteRes, err := rs.Delete(context.Background(), deleteReq)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if deleteRes == nil || deleteRes.ProgressResult == nil || deleteRes.ProgressResult.RequestID == "" {
		t.Fatalf("Delete did not return a valid RequestID")
	}
	return deleteRes
}

func TestCreate_Route53(t *testing.T) {
	t.Skip("Skipping Route53 create test - WIP")
	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	res, err := rs.Create(context.Background(), &resource.CreateRequest{
		Resource: &model.Resource{
			Label: "pel-record-snarf",
			Type:  "AWS::Route53::RecordSet",
			Stack: "pel-dns",
			Schema: model.Schema{
				Identifier: "Id",
				Tags:       "",
				Hints: map[string]model.FieldHint{
					"HostedZoneId": {
						CreateOnly: true,
						Persist:    true,
					},
					"HostedZoneName": {
						CreateOnly: true,
						Persist:    true,
					},
					"Name": {
						CreateOnly: true,
						Persist:    true,
					},
					"Type": {
						Persist: true,
					},
					"ResourceRecords": {
						Persist: true,
					},
				},
				Fields: []string{"AliasTarget", "CidrRoutingConfig", "Comment", "Failover", "GeoLocation", "GeoProximityLocation", "HealthCheckId", "HostedZoneId", "HostedZoneName", "MultiValueAnswer", "Name", "Region", "ResourceRecords", "SetIdentifier", "TTL", "Type", "Weight"},
			},
			Properties: json.RawMessage(`{"HostedZoneId":"Z03405173PGMODHWMP57N","Name":"eng.snarf.test.pel","ResourceRecords":["192.168.55.2"],"TTL":"300","Type":"A"}`),
		},
	})

	// Wait for status to be success
	if res == nil || res.ProgressResult == nil || res.ProgressResult.RequestID == "" {
		t.Fatalf("Create did not return a valid RequestID")
	}
	var statusRes *resource.StatusResult
	metadata := string(`{"HostedZoneId":"Z03405173PGMODHWMP57N","Name":"eng.snarf.test.pel","Type":"A", "ResourceRecords":["192.168.55.2"]}`)
	for {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    res.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     json.RawMessage(metadata),
		})
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			break
		}
	}

	_, err = rs.Read(context.Background(), &resource.ReadRequest{
		NativeID: statusRes.ProgressResult.NativeID,
		Metadata: json.RawMessage(metadata),
	})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	deleteReq := &resource.DeleteRequest{
		NativeID: &statusRes.ProgressResult.NativeID,
		Metadata: json.RawMessage(metadata),
	}
	_, err = rs.Delete(context.Background(), deleteReq)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

}

func TestDelete_Route53(t *testing.T) {
	t.Skip("Skipping Route53 delete test - WIP")
	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	metadata := string(`{"HostedZoneId":"Z03405173PGMODHWMP57N","Name":"eng.snarf.test.pel","Type":"A", "ResourceRecords":["192.168.55.2"]}`)

	nativeID := "Z034"
	deleteReq := &resource.DeleteRequest{
		NativeID: &nativeID,
		Metadata: json.RawMessage(metadata),
	}
	_, err := rs.Delete(context.Background(), deleteReq)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

}

func TestStatus_Route53(t *testing.T) {
	t.Skip("Skipping Route53 status test - WIP")
	_, err := awsconfig.LoadDefaultConfig(context.Background())
	assert.NoError(t, err)

	props := map[string]any{
		"AliasTarget": map[string]any{
			"DNSName":              "example.cloudfront.net",
			"EvaluateTargetHealth": false,
			"HostedZoneId":         "Z2FDTNDATAQYW2",
		},
		"HostedZoneId": "Z0019288CT0YUMA282WM",
		"Name":         "aa.test.platform.engineering",
		"TTL":          300,
		"Type":         "A",
	}
	propsBytes, _ := json.Marshal(props)

	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	_, err = rs.Status(context.Background(), &resource.StatusRequest{
		RequestID:    "/change/C0143912BN0L1VGPGYWI",
		ResourceType: "AWS::Route53::RecordSet",
		Metadata:     propsBytes,
	})

}

func TestRecordSet_Lifecycle(t *testing.T) {
	//t.Skip("Skipping Route53 record set lifecycle test - WIP")
	hostedZoneID, err := createTestHostedZone("snarf.test.pel")
	if err != nil {
		t.Fatalf("Failed to create test hosted zone: %v", err)
		return
	}
	t.Logf("Created hosted zone with ID: %s", hostedZoneID)
	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	// --- CREATE ---
	createProps := map[string]any{
		"HostedZoneId":    hostedZoneID,
		"Name":            "eng.snarf.test.pel",
		"ResourceRecords": []string{"192.168.55.2"},
		"TTL":             300,
		"Type":            "A",
	}
	createPropsBytes, _ := json.Marshal(createProps)

	createRes, err := rs.Create(context.Background(), &resource.CreateRequest{
		Resource: &model.Resource{
			Label:      "pel-record-snarf",
			Type:       "AWS::Route53::RecordSet",
			Stack:      "pel-dns",
			Properties: createPropsBytes,
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if createRes == nil || createRes.ProgressResult == nil || createRes.ProgressResult.RequestID == "" {
		t.Fatalf("Create did not return a valid RequestID")
	}

	// Wait for create to be INSYNC
	var statusRes *resource.StatusResult
	for {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    createRes.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     createPropsBytes,
		})
		if err != nil {
			t.Fatalf("Status (create) failed: %v", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// --- READ after create ---
	readRes, err := rs.Read(context.Background(), &resource.ReadRequest{
		NativeID: statusRes.ProgressResult.NativeID,
		Metadata: createPropsBytes,
	})
	if err != nil {
		t.Fatalf("Read after create failed: %v", err)
	}
	t.Logf("Created record: %s", readRes.Properties)

	// --- UPDATE ---
	updateProps := map[string]any{
		"HostedZoneId":    hostedZoneID,
		"Name":            "eng.snarf.test.pel",
		"ResourceRecords": []string{"192.168.55.3"}, // change IP
		"TTL":             600,                      // change TTL
		"Type":            "A",
	}
	updatePropsBytes, _ := json.Marshal(updateProps)

	updateRes, err := rs.Update(context.Background(), &resource.UpdateRequest{
		OldMetadata: createPropsBytes,
		Metadata:    updatePropsBytes,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updateRes == nil || updateRes.ProgressResult == nil || updateRes.ProgressResult.RequestID == "" {
		t.Fatalf("Update did not return a valid RequestID")
	}

	// Wait for update to be INSYNC
	for {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    updateRes.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     updatePropsBytes,
		})
		if err != nil {
			t.Fatalf("Status (update) failed: %v", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// --- READ after update ---
	readRes, err = rs.Read(context.Background(), &resource.ReadRequest{
		NativeID: statusRes.ProgressResult.NativeID,
		Metadata: updatePropsBytes,
	})
	if err != nil {
		t.Fatalf("Read after update failed: %v", err)
	}
	t.Logf("Updated record: %s", readRes.Properties)

	// --- DELETE ---
	deleteReq := &resource.DeleteRequest{
		Metadata: updatePropsBytes,
		NativeID: &statusRes.ProgressResult.NativeID,
	}
	deleteRes, err := rs.Delete(context.Background(), deleteReq)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if deleteRes == nil || deleteRes.ProgressResult == nil || deleteRes.ProgressResult.RequestID == "" {
		t.Fatalf("Delete did not return a valid RequestID")
	}

	// Wait for delete to be INSYNC
	for {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    deleteRes.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     updatePropsBytes,
		})
		if err != nil {
			t.Fatalf("Status (delete) failed: %v", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			break
		}
		time.Sleep(2 * time.Second)
	}

	deleteTestHostedZone(t, hostedZoneID)
}

func TestCreate_A_Record(t *testing.T) {
	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	// This simulates a record set with a $ref and $value for HostedZoneId
	props := map[string]any{
		"AliasTarget": map[string]any{
			"DNSName":              "example.cloudfront.net",
			"EvaluateTargetHealth": false,
			"HostedZoneId":         "Z2FDTNDATAQYW2",
		},
		"HostedZoneId": HostedZoneID,
		"Name":         "a." + Domain,
		"TTL":          "300",
		"Type":         "A",
	}

	propsBytes, _ := json.Marshal(props)
	metadata := string(propsBytes)

	t.Log("Creating A record set with properties:", metadata)
	createRes := create_record_set(rs, propsBytes, t)
	t.Log("Created A record set with RequestID:", createRes.ProgressResult.RequestID)

	t.Log("Waiting for create to finish: ", createRes.ProgressResult.RequestID, " to be INSYNC")
	statusRes := wait_for_status(rs, createRes.ProgressResult.RequestID, metadata, t)
	t.Log("Create completed for A record set:", createRes.ProgressResult)

	t.Log("Deleting A record set with metadata:", metadata)
	deleteRes := delete_record_set(rs, statusRes.ProgressResult.NativeID, metadata, t)
	t.Log("Delete RequestID:", deleteRes.ProgressResult.RequestID)

	t.Log("Waiting for create to finish: ", createRes.ProgressResult.RequestID, " to be INSYNC")
	wait_for_status(rs, deleteRes.ProgressResult.RequestID, metadata, t)
	t.Log("Deleted A record set with RequestID:", deleteRes.ProgressResult.RequestID)
}

var HostedZoneID = "Z03246931VP9HO8XZWUHU"
var Domain = "snarf.platform.engineering"

// TestCreate_Records tests creation and deletion for all supported Route53 record types.
func TestCreate_Records(t *testing.T) {
	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	testCases := []struct {
		name  string
		props map[string]any
	}{
		{
			name: "A record",
			props: map[string]any{
				"HostedZoneId":    HostedZoneID,
				"Name":            "a." + Domain,
				"TTL":             "300",
				"Type":            "A",
				"ResourceRecords": []string{"192.168.1.1"},
			},
		},
		{
			name: "AAAA record",
			props: map[string]any{
				"HostedZoneId":    HostedZoneID,
				"Name":            "aaaa." + Domain,
				"TTL":             "300",
				"Type":            "AAAA",
				"ResourceRecords": []string{"2001:db8::1"},
			},
		},
		{
			name: "CNAME record",
			props: map[string]any{
				"HostedZoneId":    HostedZoneID,
				"Name":            "cname." + Domain,
				"TTL":             "300",
				"Type":            "CNAME",
				"ResourceRecords": []string{"target.example.com."},
			},
		},
		{
			name: "MX record",
			props: map[string]any{
				"HostedZoneId":    HostedZoneID,
				"Name":            "mx." + Domain,
				"TTL":             "300",
				"Type":            "MX",
				"ResourceRecords": []string{"10 mail1.example.com.", "20 mail2.example.com."},
			},
		},
		{
			name: "TXT record",
			props: map[string]any{
				"HostedZoneId":    HostedZoneID,
				"Name":            "txt." + Domain,
				"TTL":             "300",
				"Type":            "TXT",
				"ResourceRecords": []string{`"v=spf1 include:example.com ~all"`},
			},
		},
		//{
		//	name: "SRV record",
		//	props: map[string]any{
		//		"HostedZoneId":    HostedZoneId,
		//		"Name":            "_sip._tcp." + Domain,
		//		"TTL":             "300",
		//		"Type":            "SRV",
		//		"ResourceRecords": []string{"10 60 5060 sipserver.example.com."},
		//	},
		//},
		//{
		//	name: "NS record",
		//	props: map[string]any{
		//		"HostedZoneId":    HostedZoneId,
		//		"Name":            "ns." + Domain,
		//		"TTL":             "300",
		//		"Type":            "NS",
		//		"ResourceRecords": []string{"ns-123.awsdns-45.com.", "ns-234.awsdns-56.net."},
		//	},
		//},
		//{
		//	name: "PTR record",
		//	props: map[string]any{
		//		"HostedZoneId":    HostedZoneId,
		//		"Name":            "1.1.168.192.in-addr.arpa",
		//		"TTL":             "300",
		//		"Type":            "PTR",
		//		"ResourceRecords": []string{"ptr." + Domain + "."},
		//	},
		//},
		//{
		//	name: "SOA record",
		//	props: map[string]any{
		//		"HostedZoneId":    HostedZoneId,
		//		"Name":            Domain + ".",
		//		"TTL":             "900",
		//		"Type":            "SOA",
		//		"ResourceRecords": []string{"ns-123.awsdns-45.com. awsdns-hostmaster.amazon.com. 1 7200 900 1209600 86400"},
		//	},
		//},
		//{
		//	name: "SPF record",
		//	props: map[string]any{
		//		"HostedZoneId":    HostedZoneId,
		//		"Name":            "spf." + Domain,
		//		"TTL":             "300",
		//		"Type":            "SPF",
		//		"ResourceRecords": []string{`"v=spf1 include:example.com ~all"`},
		//	},
		//},
		//{
		//	name: "CAA record",
		//	props: map[string]any{
		//		"HostedZoneId":    HostedZoneId,
		//		"Name":            "caa." + Domain,
		//		"TTL":             "300",
		//		"Type":            "CAA",
		//		"ResourceRecords": []string{"0 issue \"letsencrypt.org\""},
		//	},
		//},
		//{
		//	name: "Alias A record",
		//	props: map[string]any{
		//		"HostedZoneId": HostedZoneId,
		//		"Name":         "alias." + Domain,
		//		"Type":         "A",
		//		"AliasTarget": map[string]any{
		//			"DNSName":              "d123456abcdef8.cloudfront.net.",
		//			"EvaluateTargetHealth": false,
		//			"HostedZoneId":         "Z2FDTNDATAQYW2",
		//		},
		//	},
		//},
	}

	for _, tc := range testCases {
		propsBytes, _ := json.Marshal(tc.props)
		metadata := string(propsBytes)

		t.Log("Creating", tc.name, "with properties:", metadata)
		createRes := create_record_set(rs, propsBytes, t)
		t.Log("Created", tc.name, "with RequestID:", createRes.ProgressResult.RequestID)

		t.Log("Waiting for create to finish: ", createRes.ProgressResult.RequestID, " to be INSYNC")
		statusRes := wait_for_status(rs, createRes.ProgressResult.RequestID, metadata, t)
		t.Log("Create completed for", tc.name, ":", createRes.ProgressResult)

		t.Log("Deleting", tc.name, "with metadata:", metadata)
		deleteRes := delete_record_set(rs, statusRes.ProgressResult.NativeID, metadata, t)
		t.Log("Delete RequestID:", deleteRes.ProgressResult.RequestID)

		t.Log("Waiting for delete to finish: ", deleteRes.ProgressResult.RequestID, " to be INSYNC")
		wait_for_status(rs, deleteRes.ProgressResult.RequestID, metadata, t)
		t.Log("Deleted", tc.name, "with RequestID:", deleteRes.ProgressResult.RequestID)
	}
}

func TestCreate_RecordSet_2(t *testing.T) {
	t.Skip("Skipping Route53 record set test - WIP")
	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	// This simulates a record set with a $ref and $value for HostedZoneId
	props := map[string]any{
		"AliasTarget": map[string]any{
			"DNSName":              "d123456abcdef8.cloudfront.net",
			"EvaluateTargetHealth": false,
			"HostedZoneId":         "Z2FDTNDATAQYW2",
		},
		"HostedZoneId": "Z07395323V6QPG5XX24K3",
		"Name":         "test.platform.engineering",
		"Type":         "A",
	}
	propsBytes, _ := json.Marshal(props)

	createRes, err := rs.Create(context.Background(), &resource.CreateRequest{
		Resource: &model.Resource{
			Label:      "pel-record-resolvable",
			Type:       "AWS::Route53::RecordSet",
			Stack:      "pel-dns",
			Properties: propsBytes,
			Schema: model.Schema{
				Hints: map[string]model.FieldHint{
					"AliasTarget": {
						Persist: true,
					},
					"HostedZoneId": {
						Persist: true,
					},
					"Name": {
						Persist: true,
					},
					"ResourceRecords": {
						Persist: true,
					},
					"TTL": {
						Persist: true,
					},
					"Type": {
						Persist: true,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if createRes == nil || createRes.ProgressResult == nil || createRes.ProgressResult.RequestID == "" {
		t.Fatalf("Create did not return a valid RequestID")
	}

	// Wait for create to be INSYNC
	var statusRes *resource.StatusResult
	for {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    createRes.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     createRes.ProgressResult.Metadata,
		})
		if err != nil {
			t.Fatalf("Status (create) failed: %v", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// --- READ after create ---
	_, err = rs.Read(context.Background(), &resource.ReadRequest{
		NativeID: statusRes.ProgressResult.NativeID,
		Metadata: propsBytes,
	})
	if err != nil {
		t.Fatalf("Read after create failed: %v", err)
	}

	// --- DELETE ---
	deleteReq := &resource.DeleteRequest{
		Metadata: propsBytes,
	}
	deleteRes, err := rs.Delete(context.Background(), deleteReq)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if deleteRes == nil || deleteRes.ProgressResult == nil || deleteRes.ProgressResult.RequestID == "" {
		t.Fatalf("Delete did not return a valid RequestID")
	}

	// Wait for delete to be INSYNC
	for {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    deleteRes.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     propsBytes,
		})
		if err != nil {
			t.Fatalf("Status (delete) failed: %v", err)
		}
		if statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess {
			break
		}
		time.Sleep(2 * time.Second)
	}

}

func TestRecordSet_ListRecordSets(t *testing.T) {
	hostedZoneID, err := createTestHostedZone("snarf.test.pel")
	assert.NoError(t, err)
	cfg := &config.Config{}
	rs := RecordSet{cfg: cfg}

	// create two recordsets in AWS (a hosted zone comes with two default recordsets)
	nativeID1 := createRecordSet(t, rs, hostedZoneID, "eng1.snarf.test.pel", []string{"192.168.55.2"}, "300", "A")
	nativeID2 := createRecordSet(t, rs, hostedZoneID, "eng2.snarf.test.pel", []string{"192.168.55.3"}, "300", "A")

	defer func() {
		// delete the record sets
		deleteRecordSet(t, rs, hostedZoneID, nativeID1, "eng1.snarf.test.pel", []string{"192.168.55.2"}, "300", "A")
		deleteRecordSet(t, rs, hostedZoneID, nativeID2, "eng2.snarf.test.pel", []string{"192.168.55.3"}, "300", "A")

		// delete hosted zone
		deleteTestHostedZone(t, hostedZoneID)
	}()

	var pageSize int32 = 2

	// expect 2 pages of 2 recordsets each
	firstPage, err := rs.List(context.Background(), &resource.ListRequest{
		ResourceType: "AWS::Route53::RecordSet",
		PageSize:     pageSize,
		AdditionalProperties: map[string]string{
			"HostedZoneId": hostedZoneID,
		},
	})

	assert.NoError(t, err)
	assert.Len(t, firstPage.Resources, 2)
	assert.NotEmpty(t, firstPage.NextPageToken)

	secondPage, err := rs.List(context.Background(), &resource.ListRequest{
		ResourceType: "AWS::Route53::RecordSet",
		PageSize:     pageSize,
		PageToken:    firstPage.NextPageToken,
		AdditionalProperties: map[string]string{
			"HostedZoneId": hostedZoneID,
		},
	})

	assert.NoError(t, err)
	assert.Len(t, secondPage.Resources, 2)
	assert.Empty(t, secondPage.NextPageToken)
}

func createRecordSet(t *testing.T, rs RecordSet, hostedZoneID string, name string, records []string, ttl string, recordType string) string {
	props := map[string]any{
		"HostedZoneId":    hostedZoneID,
		"Name":            name,
		"ResourceRecords": records,
		"TTL":             ttl,
		"Type":            recordType,
	}
	propsBytes, _ := json.Marshal(props)

	createRes, err := rs.Create(context.Background(), &resource.CreateRequest{
		Resource: &model.Resource{
			Label:      fmt.Sprintf("pel-record-snarf-%s", uuid.New().String()),
			Type:       "AWS::Route53::RecordSet",
			Stack:      "pel-dns",
			Properties: propsBytes,
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if createRes == nil || createRes.ProgressResult == nil || createRes.ProgressResult.RequestID == "" {
		t.Fatalf("Create did not return a valid RequestID")
	}

	var statusRes *resource.StatusResult
	assert.Eventually(t, func() bool {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    createRes.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     propsBytes,
		})
		if err != nil {
			t.Fatalf("Status (create) failed: %v", err)
		}
		return statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess
	}, 2*time.Minute, 500*time.Millisecond)

	return statusRes.ProgressResult.NativeID
}

func deleteRecordSet(t *testing.T, rs RecordSet, hostedZoneID string, nativeID, name string, records []string, ttl string, recordType string) {
	props := map[string]any{
		"HostedZoneId":    hostedZoneID,
		"Name":            name,
		"ResourceRecords": records,
		"TTL":             ttl,
		"Type":            recordType,
	}
	propsBytes, _ := json.Marshal(props)

	req := &resource.DeleteRequest{
		Metadata: propsBytes,
		NativeID: &nativeID,
	}
	deleteRes, err := rs.Delete(context.Background(), req)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if deleteRes == nil || deleteRes.ProgressResult == nil || deleteRes.ProgressResult.RequestID == "" {
		t.Fatalf("Delete did not return a valid RequestID")
	}

	var statusRes *resource.StatusResult
	assert.Eventually(t, func() bool {
		statusRes, err = rs.Status(context.Background(), &resource.StatusRequest{
			RequestID:    deleteRes.ProgressResult.RequestID,
			ResourceType: "AWS::Route53::RecordSet",
			Metadata:     propsBytes,
		})
		if err != nil {
			t.Fatalf("Status (delete) failed: %v", err)
		}

		return statusRes.ProgressResult.OperationStatus == resource.OperationStatusSuccess
	}, 2*time.Minute, 500*time.Millisecond)

	t.Logf("Delete completed: %+v", statusRes.ProgressResult)
}
