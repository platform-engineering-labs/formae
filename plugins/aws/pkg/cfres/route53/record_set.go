// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package route53

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"

	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/prov"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/registry"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/utils"
)

type RecordSet struct {
	cfg *config.Config
}

type MetaDataRecordSet struct {
	HostedZoneID    string   `json:"HostedZoneId"`
	Name            string   `json:"Name"`
	Type            string   `json:"Type"`
	ResourceRecords []string `json:"ResourceRecords,omitempty"`
	TTL             int64    `json:"-"` // Don't unmarshal directly
	AliasTarget     *struct {
		DNSName              string `json:"DNSName"`
		HostedZoneID         string `json:"HostedZoneId"`
		EvaluateTargetHealth bool   `json:"EvaluateTargetHealth"`
	} `json:"AliasTarget,omitempty"`
}

// UnmarshalJSON implements custom JSON unmarshaling to handle TTL as string or int
func (m *MetaDataRecordSet) UnmarshalJSON(data []byte) error {
	// Use an auxiliary struct to unmarshal the JSON
	type Alias MetaDataRecordSet
	aux := &struct {
		TTL any `json:"TTL,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(m),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Handle TTL conversion
	if aux.TTL != nil {
		switch v := aux.TTL.(type) {
		case string:
			if v != "" {
				ttl, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid TTL string value: %v", v)
				}
				m.TTL = ttl
			}
		case float64:
			m.TTL = int64(v)
		case int64:
			m.TTL = v
		case int:
			m.TTL = int64(v)
		default:
			return fmt.Errorf("invalid TTL type: %T", v)
		}
	}

	return nil
}

func (m *MetaDataRecordSet) MarshalJSON() ([]byte, error) {
	type Alias MetaDataRecordSet
	return json.Marshal(&struct {
		TTL int64 `json:"TTL,omitempty"`
		*Alias
	}{
		TTL:   m.TTL,
		Alias: (*Alias)(m),
	})
}
func (m *MetaDataRecordSet) NativeID() string {
	name := m.Name
	if !strings.HasSuffix(name, ".") {
		name = name + "."
	}
	return fmt.Sprintf("%s|%s|%s", m.HostedZoneID, name, m.Type)
}

// ParseMetaDataRecordSet parses a metadata JSON string into a MetaDataRecordSet struct.
func ParseMetaDataRecordSet(meta json.RawMessage) (*MetaDataRecordSet, error) {
	var metaData MetaDataRecordSet
	if err := json.Unmarshal(meta, &metaData); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %s %w", string(meta), err)
	}
	return &metaData, nil
}

var _ prov.Provisioner = &RecordSet{}

func init() {
	registry.Register("AWS::Route53::RecordSet",
		[]resource.Operation{
			resource.OperationRead,
			resource.OperationCreate,
			resource.OperationUpdate,
			resource.OperationCheckStatus,
			resource.OperationDelete,
			resource.OperationList},
		func(cfg *config.Config) prov.Provisioner {
			return &RecordSet{cfg: cfg}
		})
}

func (r RecordSet) Create(ctx context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}

	client := route53.NewFromConfig(cfg)

	// Parse properties from JSON
	var properties map[string]any
	if err := json.Unmarshal(request.Resource.Properties, &properties); err != nil {
		return nil, fmt.Errorf("failed to parse properties: %w", err)
	}

	// Extract required properties with validation
	hostedZoneID, err := utils.GetStringProperty(properties, "HostedZoneId")
	if err != nil {
		return nil, fmt.Errorf("invalid HostedZoneId: %w", err)
	}

	name, err := utils.GetStringProperty(properties, "Name")
	if err != nil {
		return nil, fmt.Errorf("invalid Name: %w", err)
	}

	recordType, err := utils.GetStringProperty(properties, "Type")
	if err != nil {
		return nil, fmt.Errorf("invalid Type: %w", err)
	}

	// Get TTL with default value
	ttl := utils.GetInt64Property(properties, "TTL", 300)

	var aliasTarget *types.AliasTarget
	if aliasTargetRaw, hasAlias := properties["AliasTarget"].(map[string]any); hasAlias {
		dnsName, err := utils.GetStringProperty(aliasTargetRaw, "DNSName")
		if err != nil {
			return nil, fmt.Errorf("invalid AliasTarget DNSName: %w", err)
		}

		hostedZoneID, err := utils.GetStringProperty(aliasTargetRaw, "HostedZoneId")
		if err != nil {
			return nil, fmt.Errorf("invalid AliasTarget HostedZoneId: %w", err)
		}

		evaluateTargetHealth := false
		if evalHealth, ok := aliasTargetRaw["EvaluateTargetHealth"].(bool); ok {
			evaluateTargetHealth = evalHealth
		}

		aliasTarget = &types.AliasTarget{
			DNSName:              aws.String(dnsName),
			HostedZoneId:         aws.String(hostedZoneID),
			EvaluateTargetHealth: evaluateTargetHealth,
		}
	}

	// Extract and validate resource records
	var records []types.ResourceRecord
	if aliasTarget == nil {
		if resourceRecordsRaw, ok := properties["ResourceRecords"].([]any); ok {
			records = make([]types.ResourceRecord, 0, len(resourceRecordsRaw))
			for _, record := range resourceRecordsRaw {
				if value, ok := record.(string); ok && value != "" {
					records = append(records, types.ResourceRecord{
						Value: aws.String(value),
					})
				}
			}
		}
		if len(records) == 0 {
			return nil, fmt.Errorf("at least one valid ResourceRecord is required")
		}
	}

	rrs := &types.ResourceRecordSet{
		Name: aws.String(name),
		Type: types.RRType(recordType),
	}

	if aliasTarget != nil {
		rrs.AliasTarget = aliasTarget
	} else {
		rrs.ResourceRecords = records
		rrs.TTL = aws.Int64(ttl)
	}

	// Create the record set
	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(hostedZoneID),
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action:            types.ChangeActionCreate,
					ResourceRecordSet: rrs,
				},
			},
		},
	}

	result, err := client.ChangeResourceRecordSets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to create record set: %w", err)
	}
	metadata, err := request.Resource.GetMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCreate,
			OperationStatus: resource.OperationStatusInProgress,
			RequestID:       *result.ChangeInfo.Id,
			Metadata:        metadata,
		},
	}, nil
}

func (r RecordSet) Update(ctx context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}

	client := route53.NewFromConfig(cfg)

	// Parse metadata from JSON for both old and new states
	var oldMetadata, newMetadata map[string]any
	if err := json.Unmarshal(request.OldMetadata, &oldMetadata); err != nil {
		return nil, fmt.Errorf("failed to parse old metadata: %w", err)
	}
	if err := json.Unmarshal(request.Metadata, &newMetadata); err != nil {
		return nil, fmt.Errorf("failed to parse new metadata: %w", err)
	}

	// Extract required metadata for both states
	oldHostedZoneID, err := utils.GetStringProperty(oldMetadata, "HostedZoneId")
	if err != nil {
		return nil, fmt.Errorf("invalid old HostedZoneId: %w", err)
	}
	newHostedZoneID, err := utils.GetStringProperty(newMetadata, "HostedZoneId")
	if err != nil {
		return nil, fmt.Errorf("invalid new HostedZoneId: %w", err)
	}

	// Verify hosted zone IDs match (can't move between zones)
	if oldHostedZoneID != newHostedZoneID {
		return nil, fmt.Errorf("cannot update record between different hosted zones")
	}

	// Create old ResourceRecordSet
	oldName, err := utils.GetStringProperty(oldMetadata, "Name")
	if err != nil {
		return nil, fmt.Errorf("invalid old Name: %w", err)
	}
	oldType, err := utils.GetStringProperty(oldMetadata, "Type")
	if err != nil {
		return nil, fmt.Errorf("invalid old Type: %w", err)
	}
	oldTTL := utils.GetInt64Property(oldMetadata, "TTL", 300)

	// Create new ResourceRecordSet
	newName, err := utils.GetStringProperty(newMetadata, "Name")
	if err != nil {
		return nil, fmt.Errorf("invalid new Name: %w", err)
	}
	newType, err := utils.GetStringProperty(newMetadata, "Type")
	if err != nil {
		return nil, fmt.Errorf("invalid new Type: %w", err)
	}
	newTTL := utils.GetInt64Property(newMetadata, "TTL", 300)

	// Handle old AliasTarget
	var oldAliasTarget *types.AliasTarget
	if oldAliasTargetRaw, hasAlias := oldMetadata["AliasTarget"].(map[string]any); hasAlias {
		dnsName, err := utils.GetStringProperty(oldAliasTargetRaw, "DNSName")
		if err != nil {
			return nil, fmt.Errorf("invalid old AliasTarget DNSName: %w", err)
		}
		hostedZoneID, err := utils.GetStringProperty(oldAliasTargetRaw, "HostedZoneId")
		if err != nil {
			return nil, fmt.Errorf("invalid old AliasTarget HostedZoneId: %w", err)
		}
		oldAliasTarget = &types.AliasTarget{
			DNSName:              aws.String(dnsName),
			HostedZoneId:         aws.String(hostedZoneID),
			EvaluateTargetHealth: false,
		}
	}

	// Handle new AliasTarget
	var newAliasTarget *types.AliasTarget
	if newAliasTargetRaw, hasAlias := newMetadata["AliasTarget"].(map[string]any); hasAlias {
		dnsName, err := utils.GetStringProperty(newAliasTargetRaw, "DNSName")
		if err != nil {
			return nil, fmt.Errorf("invalid new AliasTarget DNSName: %w", err)
		}
		hostedZoneID, err := utils.GetStringProperty(newAliasTargetRaw, "HostedZoneId")
		if err != nil {
			return nil, fmt.Errorf("invalid new AliasTarget HostedZoneId: %w", err)
		}
		newAliasTarget = &types.AliasTarget{
			DNSName:              aws.String(dnsName),
			HostedZoneId:         aws.String(hostedZoneID),
			EvaluateTargetHealth: false,
		}
	}

	// Handle ResourceRecords for old and new if not using AliasTarget
	var oldRecords, newRecords []types.ResourceRecord
	if oldAliasTarget == nil {
		if oldResourceRecordsRaw, ok := oldMetadata["ResourceRecords"].([]any); ok {
			oldRecords = make([]types.ResourceRecord, 0, len(oldResourceRecordsRaw))
			for _, record := range oldResourceRecordsRaw {
				if value, ok := record.(string); ok && value != "" {
					oldRecords = append(oldRecords, types.ResourceRecord{
						Value: aws.String(value),
					})
				}
			}
		}
		if len(oldRecords) == 0 {
			return nil, fmt.Errorf("at least one valid ResourceRecord is required for old record when not using AliasTarget")
		}
	}

	if newAliasTarget == nil {
		if newResourceRecordsRaw, ok := newMetadata["ResourceRecords"].([]any); ok {
			newRecords = make([]types.ResourceRecord, 0, len(newResourceRecordsRaw))
			for _, record := range newResourceRecordsRaw {
				if value, ok := record.(string); ok && value != "" {
					newRecords = append(newRecords, types.ResourceRecord{
						Value: aws.String(value),
					})
				}
			}
		}
		if len(newRecords) == 0 {
			return nil, fmt.Errorf("at least one valid ResourceRecord is required for new record when not using AliasTarget")
		}
	}

	// Create the old and new ResourceRecordSets
	oldRrs := &types.ResourceRecordSet{
		Name: aws.String(oldName),
		Type: types.RRType(oldType),
	}
	if oldAliasTarget != nil {
		oldRrs.AliasTarget = oldAliasTarget
	} else {
		oldRrs.ResourceRecords = oldRecords
		oldRrs.TTL = aws.Int64(oldTTL)
	}

	newRrs := &types.ResourceRecordSet{
		Name: aws.String(newName),
		Type: types.RRType(newType),
	}
	if newAliasTarget != nil {
		newRrs.AliasTarget = newAliasTarget
	} else {
		newRrs.ResourceRecords = newRecords
		newRrs.TTL = aws.Int64(newTTL)
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(newHostedZoneID),
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action:            types.ChangeActionDelete,
					ResourceRecordSet: oldRrs,
				},
				{
					Action:            types.ChangeActionCreate,
					ResourceRecordSet: newRrs,
				},
			},
		},
	}

	result, err := client.ChangeResourceRecordSets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to update record set: %w", err)
	}

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationUpdate,
			OperationStatus: resource.OperationStatusInProgress,
			RequestID:       *result.ChangeInfo.Id,
		},
	}, nil
}

func (r RecordSet) Delete(ctx context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}

	client := route53.NewFromConfig(cfg)

	// Always read the current record before attempting delete to get the exact state
	readRes, err := r.Read(ctx, &resource.ReadRequest{
		NativeID: *request.NativeID,
		Metadata: request.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read record before delete: %w", err)
	}

	if readRes.ErrorCode == resource.OperationErrorCodeNotFound {
		// Route does not exist, nothing to delete
		return &resource.DeleteResult{
			ProgressResult: &resource.ProgressResult{
				Operation:       resource.OperationDelete,
				OperationStatus: resource.OperationStatusSuccess,
				NativeID:        *request.NativeID,
			},
		}, nil
	}

	meta, err := ParseMetaDataRecordSet(json.RawMessage(readRes.Properties))
	if err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	hostedZoneID := meta.HostedZoneID
	name := meta.Name
	if !strings.HasSuffix(name, ".") {
		name = name + "."
	}
	recordType := meta.Type

	rrs := &types.ResourceRecordSet{
		Name: aws.String(name),
		Type: types.RRType(recordType),
	}

	// Handle AliasTarget if present
	if meta.AliasTarget != nil {
		dnsName := meta.AliasTarget.DNSName
		if !strings.HasSuffix(dnsName, ".") {
			dnsName = dnsName + "."
		}
		rrs.AliasTarget = &types.AliasTarget{
			DNSName:              aws.String(dnsName),
			HostedZoneId:         aws.String(meta.AliasTarget.HostedZoneID),
			EvaluateTargetHealth: meta.AliasTarget.EvaluateTargetHealth,
		}
	} else {
		rrs.TTL = aws.Int64(meta.TTL)
		if len(meta.ResourceRecords) > 0 {
			records := make([]types.ResourceRecord, 0, len(meta.ResourceRecords))
			for _, value := range meta.ResourceRecords {
				if value != "" {
					records = append(records, types.ResourceRecord{
						Value: aws.String(value),
					})
				}
			}
			rrs.ResourceRecords = records
		}
	}

	input := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(hostedZoneID),
		ChangeBatch: &types.ChangeBatch{
			Changes: []types.Change{
				{
					Action:            types.ChangeActionDelete,
					ResourceRecordSet: rrs,
				},
			},
		},
	}

	result, err := client.ChangeResourceRecordSets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to delete record set: %w", err)
	}

	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationDelete,
			OperationStatus:    resource.OperationStatusInProgress,
			RequestID:          *result.ChangeInfo.Id,
			ResourceType:       request.ResourceType,
			NativeID:           meta.NativeID(),
			ResourceProperties: json.RawMessage{},
		},
	}, nil
}

func (r RecordSet) Status(ctx context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}

	client := route53.NewFromConfig(cfg)

	input := &route53.GetChangeInput{
		Id: aws.String(request.RequestID),
	}

	result, err := client.GetChange(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get change status: %w", err)
	}

	nativeID := ""
	status := resource.OperationStatusInProgress
	var resourceProperties json.RawMessage

	if result.ChangeInfo.Status == types.ChangeStatusInsync {
		status = resource.OperationStatusSuccess
		// Use MetaDataRecordSet for nativeID and read
		if len(request.Metadata) > 0 && string(request.Metadata) != "{}" {
			meta, err := ParseMetaDataRecordSet(request.Metadata)
			if err != nil {
				return nil, fmt.Errorf("failed to parse metadata: %w", err)
			}
			if meta.HostedZoneID != "" && meta.Name != "" && meta.Type != "" {
				nativeID = meta.NativeID()
				readRes, readErr := r.Read(ctx, &resource.ReadRequest{
					NativeID: nativeID,
					Metadata: request.Metadata,
				})
				if readErr == nil && readRes != nil {
					resourceProperties = json.RawMessage(readRes.Properties)
					return &resource.StatusResult{
						ProgressResult: &resource.ProgressResult{
							OperationStatus:    status,
							RequestID:          *result.ChangeInfo.Id,
							NativeID:           nativeID,
							ResourceProperties: resourceProperties,
							ResourceType:       request.ResourceType,
						},
					}, nil
				}
			}
		}
	}

	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			OperationStatus:    status,
			RequestID:          *result.ChangeInfo.Id,
			NativeID:           nativeID,
			ResourceProperties: resourceProperties,
			ResourceType:       request.ResourceType,
		},
	}, nil
}

func (r RecordSet) Read(ctx context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}
	client := route53.NewFromConfig(cfg)

	var hostedZoneID, name, recordType string

	// Prefer metadata if available
	if len(request.Metadata) > 0 && string(request.Metadata) != "{}" {
		meta, err := ParseMetaDataRecordSet(request.Metadata)
		if err == nil {
			hostedZoneID = meta.HostedZoneID
			name = meta.Name
			recordType = meta.Type
			if !strings.HasSuffix(name, ".") {
				name = name + "."
			}
		}
	}

	// Fallback to NativeID if needed
	if hostedZoneID == "" || name == "" || recordType == "" {
		parts := strings.SplitN(request.NativeID, "|", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid NativeID format: expected 'zoneId|name|type', got: %s", request.NativeID)
		}
		hostedZoneID = parts[0]
		name = parts[1]
		recordType = parts[2]
	}

	// Query the record set
	resp, err := client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(hostedZoneID),
		StartRecordName: aws.String(name),
		StartRecordType: types.RRType(recordType),
	})
	if err != nil {
		//return nil, fmt.Errorf("failed to list resource record sets: %w", err)
		return &resource.ReadResult{
			ResourceType: request.ResourceType,
			ErrorCode:    resource.OperationErrorCodeNotFound,
		}, nil
	}

	// Find exact match
	var found *types.ResourceRecordSet
	for _, rrs := range resp.ResourceRecordSets {
		if aws.ToString(rrs.Name) == name && string(rrs.Type) == recordType {
			found = &rrs
			break
		}
	}
	if found == nil {
		//return nil, fmt.Errorf("record not found: %s %s in zone %s", name, recordType, hostedZoneId)
		return &resource.ReadResult{
			ResourceType: request.ResourceType,
			ErrorCode:    resource.OperationErrorCodeNotFound,
		}, nil
	}

	// Build properties map
	props := map[string]any{
		"HostedZoneId": hostedZoneID,
		"Name":         strings.TrimSuffix(name, "."), // remove trailing dot
		"Type":         recordType,
	}

	if found.AliasTarget != nil {
		props["AliasTarget"] = map[string]any{
			"DNSName":              aws.ToString(found.AliasTarget.DNSName),
			"HostedZoneId":         aws.ToString(found.AliasTarget.HostedZoneId),
			"EvaluateTargetHealth": found.AliasTarget.EvaluateTargetHealth,
		}
	} else {
		records := make([]string, 0, len(found.ResourceRecords))
		for _, rr := range found.ResourceRecords {
			records = append(records, aws.ToString(rr.Value))
		}
		props["ResourceRecords"] = records
		if found.TTL != nil {
			props["TTL"] = *found.TTL
		}
	}

	// Marshal back to JSON
	propBytes, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal properties: %w", err)
	}

	return &resource.ReadResult{
		ResourceType: "AWS::Route53::RecordSet",
		Properties:   string(propBytes),
	}, nil
}

func (r *RecordSet) List(ctx context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	cfg, err := r.cfg.ToAwsConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS config: %w", err)
	}
	client := route53.NewFromConfig(cfg)

	hostedZoneID, ok := request.AdditionalProperties["HostedZoneId"]
	if !ok || hostedZoneID == "" {
		return nil, fmt.Errorf("hostedZoneId must be provided in AdditionalProperties for listing record sets")
	}
	res, err := client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId:    &hostedZoneID,
		MaxItems:        &request.PageSize,
		StartRecordName: request.PageToken,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list resource record sets: %w", err)
	}

	var resources []resource.Resource
	for _, rrs := range res.ResourceRecordSets {
		resources = append(resources, resource.Resource{
			NativeID:   nativeID(request.AdditionalProperties["HostedZoneId"], *rrs.Name, string(rrs.Type)),
			Properties: "{}", // Properties are ignored in list, we rely on a subsequent read to fetch them
		})
	}

	return &resource.ListResult{
		ResourceType:  request.ResourceType,
		Resources:     resources,
		NextPageToken: res.NextRecordName,
	}, nil
}

func nativeID(hostedZoneID, name, recordType string) string {
	if !strings.HasSuffix(name, ".") {
		name = name + "."
	}
	return fmt.Sprintf("%s|%s|%s", hostedZoneID, name, recordType)
}
