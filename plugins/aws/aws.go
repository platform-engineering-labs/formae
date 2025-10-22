// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/masterminds/semver"

	"github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/ccx"
	_ "github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/cfres/registry"
	"github.com/platform-engineering-labs/formae/plugins/aws/pkg/config"
	descriptors "github.com/platform-engineering-labs/formae/plugins/aws/pkg/descriptors"
)

type AWS struct{}

// Version set at compile time
var Version = "0.0.0"

// Compile time checks to satisfy protocol
var _ plugin.Plugin = AWS{}
var _ plugin.ResourcePlugin = AWS{}

// Plugin maintains the known symbol reference
var Plugin = AWS{}

var Descriptors []plugin.ResourceDescriptor

var ResourceTypeDescriptors map[string]descriptors.TypeInformation

// EKSAutomodeResourceTypes lists AWS CloudFormation resource types that EKS Automode manages
var EKSAutomodeResourceTypes = []string{
	"AWS::EC2::Instance",                    	 // Worker nodes
	"AWS::EC2::SecurityGroup",               	 // Pod and node security groups
	"AWS::EC2::NetworkInterface",            	 // ENIs for pod networking
	"AWS::EC2::LaunchTemplate",              	 // Instance configuration templates
	"AWS::AutoScaling::AutoScalingGroup",    	 // For scaling worker nodes
	"AWS::EC2::VPCEndpoint",                 	 // If using private API access
	"AWS::EC2::RouteTable",                  	 // If creating custom routing
	"AWS::EC2::Subnet",                      	 // If creating new subnets 
	"AWS::EC2::Volume",                      	 // EBS volumes for persistent storage
	"AWS::EFS::FileSystem",                  	 // If using EFS for persistent storage
	"AWS::EFS::MountTarget",                 	 // If using EFS
	"AWS::IAM::Role",                        	 // Service accounts and pod execution roles
	"AWS::IAM::InstanceProfile",             	 // EC2 instance permissions
	"AWS::ElasticLoadBalancingV2::LoadBalancer", // If using ALB/NLB
	"AWS::ElasticLoadBalancingV2::TargetGroup",  // If using ALB/NLB
	"AWS::Logs::LogGroup",                       // If using CloudWatch logging
}

func init() {
	// Load descriptors from PKL file
	ctx := context.Background()
	var err error
	ResourceTypeDescriptors, err = descriptors.LoadDescriptors(ctx)
	if err != nil {
		panic("Failed to load resource descriptors: " + err.Error())
	}

	// Convert to plugin.ResourceDescriptor
	Descriptors = make([]plugin.ResourceDescriptor, 0, len(ResourceTypeDescriptors))
	for typeName := range ResourceTypeDescriptors {
		Descriptors = append(Descriptors, plugin.ResourceDescriptor{
			Type:                                     typeName,
			Discoverable:                             ResourceTypeDescriptors[typeName].Discoverable,
			ParentResourceTypesWithMappingProperties: ResourceTypeDescriptors[typeName].ParentResourcesWithMappingProperties,
		})
	}
}

func (a AWS) Name() string {
	return "aws"
}

func (a AWS) Version() *semver.Version {
	return semver.MustParse(Version)
}

func (a AWS) Type() plugin.Type {
	return plugin.Resource
}

func (a AWS) Namespace() string {
	return "AWS"
}

func (a AWS) SupportedResources() []plugin.ResourceDescriptor {
	return Descriptors
}

func (a AWS) MaxRequestsPerSecond() int {
	return 2
}

func (a AWS) SchemaForResourceType(resourceType string) (model.Schema, error) {
	if typeInfo, ok := ResourceTypeDescriptors[resourceType]; ok {
		return typeInfo.Schema, nil
	}
	return model.Schema{}, nil
}

func (a AWS) Create(context context.Context, request *resource.CreateRequest) (*resource.CreateResult, error) {
	targetConfig := config.FromTarget(request.Target)
	if registry.HasProvisioner(request.Resource.Type, resource.OperationCreate) {
		provisioner := registry.Get(request.Resource.Type, resource.OperationCreate, targetConfig)
		return provisioner.Create(context, request)
	}

	client, err := ccx.NewClient(targetConfig)
	if err != nil {
		return nil, err
	}

	return client.CreateResource(context, request)
}

func (a AWS) Update(context context.Context, request *resource.UpdateRequest) (*resource.UpdateResult, error) {
	if registry.HasProvisioner(request.Resource.Type, resource.OperationUpdate) {
		provisioner := registry.Get(request.Resource.Type, resource.OperationUpdate, config.FromTarget(request.Target))
		return provisioner.Update(context, request)
	}

	client, err := ccx.NewClient(config.FromTarget(request.Target))
	if err != nil {
		return nil, err
	}

	return client.UpdateResource(context, request)
}

func (a AWS) Status(context context.Context, request *resource.StatusRequest) (*resource.StatusResult, error) {
	if request.ResourceType != "" {
		if registry.HasProvisioner(request.ResourceType, resource.OperationCheckStatus) {
			provisioner := registry.Get(request.ResourceType, resource.OperationCheckStatus, config.FromTarget(request.Target))
			return provisioner.Status(context, request)
		}
	}

	client, err := ccx.NewClient(config.FromTarget(request.Target))
	if err != nil {
		return nil, err
	}

	return client.StatusResource(context, request, a.Read)
}

func (a AWS) Delete(context context.Context, request *resource.DeleteRequest) (*resource.DeleteResult, error) {
	if registry.HasProvisioner(request.ResourceType, resource.OperationDelete) {
		provisioner := registry.Get(request.ResourceType, resource.OperationDelete, config.FromTarget(request.Target))
		return provisioner.Delete(context, request)
	}

	client, err := ccx.NewClient(config.FromTarget(request.Target))
	if err != nil {
		return nil, err
	}

	return client.DeleteResource(context, request)
}

func (a AWS) Read(context context.Context, request *resource.ReadRequest) (*resource.ReadResult, error) {
	if registry.HasProvisioner(request.ResourceType, resource.OperationRead) {
		provisioner := registry.Get(request.ResourceType, resource.OperationRead, config.FromTarget(request.Target))
		return provisioner.Read(context, request)
	}

	client, err := ccx.NewClient(config.FromTarget(request.Target))
	if err != nil {
		return nil, err
	}

	return client.ReadResource(context, request)
}

func (a AWS) List(context context.Context, request *resource.ListRequest) (*resource.ListResult, error) {
	if registry.HasProvisioner(request.ResourceType, resource.OperationList) {
		provisioner := registry.Get(request.ResourceType, resource.OperationList, config.FromTarget(request.Target))
		return provisioner.List(context, request)
	}

	client, err := ccx.NewClient(config.FromTarget(request.Target))
	if err != nil {
		return nil, err
	}

	var resourceModel *string
	if len(request.AdditionalProperties) > 0 {
		jsonBytes, err := json.Marshal(request.AdditionalProperties)
		if err != nil {
			return nil, err
		}
		resourceModelStr := string(jsonBytes)
		resourceModel = &resourceModelStr
	}
	var resources []resource.Resource
	result, err := client.ListResources(context, &cloudcontrol.ListResourcesInput{TypeName: &request.ResourceType, MaxResults: &request.PageSize, NextToken: request.PageToken, ResourceModel: resourceModel})
	if err != nil {
		return nil, err
	}
	for _, r := range result.ResourceDescriptions {
		resources = append(resources, resource.Resource{
			NativeID:   *r.Identifier,
			Properties: *r.Properties,
		})
	}

	return &resource.ListResult{
		ResourceType:  request.ResourceType,
		Resources:     resources,
		NextPageToken: result.NextToken,
	}, nil
}

func (a AWS) TargetBehavior() resource.TargetBehavior {
	return TargetBehavior
}

// GetResourceFilters returns a map of resource types to filter functions for EKS Automode resources
func (a AWS) GetResourceFilters() map[string]plugin.ResourceFilter {
	filters := make(map[string]plugin.ResourceFilter)
	
	for _, resourceType := range EKSAutomodeResourceTypes {
		filters[resourceType] = func(properties json.RawMessage, target model.Target) bool {
			return a.shouldFilterEKSAutomodeResource(properties, target)
		}
	}
	
	return filters
}

// shouldFilterEKSAutomodeResource filters out resources that are managed by EKS Automode
func (a AWS) shouldFilterEKSAutomodeResource(properties json.RawMessage, target model.Target) bool {
	tags := model.GetTagsFromProperties(properties)
	if len(tags) == 0 {
		return false
	}
	
	clusterNames, err := a.GetAutomodeClusterNames(context.Background(), config.FromTarget(&target))
	if err != nil {
		slog.Debug("Error getting Automode cluster names", "error", err)
		return false
	}
		
	for _, clusterName := range clusterNames {
		clusterTagKey := "kubernetes.io/cluster/" + clusterName
		
		// Check for kubernetes.io/cluster/{clusterName} tag
		for _, tag := range tags {
			normalizedTagKey := strings.ToLower(tag.Key)
			normalizedClusterTagKey := strings.ToLower(clusterTagKey)
			
			if normalizedTagKey == normalizedClusterTagKey && tag.Value == "owned" {
				return true
			}
		}
	}
	
	return false
}

// GetAutomodeClusterNames returns a list of EKS cluster names that have Automode enabled
func (a AWS) GetAutomodeClusterNames(ctx context.Context, targetConfig *config.Config) ([]string, error) {
	awsCfg, err := targetConfig.ToAwsConfig(ctx)
	if err != nil {
		return nil, err
	}
	eksClient := eks.NewFromConfig(awsCfg)
	
	listResult, err := eksClient.ListClusters(ctx, &eks.ListClustersInput{})
	if err != nil {
		return nil, err
	}
	
	var automodeClusters []string
	for _, clusterName := range listResult.Clusters {
		describeResult, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
			Name: &clusterName,
		})
		if err != nil {
			slog.Error("Error describing cluster", "clusterName", clusterName, "error", err)
			continue
		}
		
		// Automode is enabled when a cluster has ComputeConfig enabled
		if describeResult.Cluster.ComputeConfig != nil && 
		   describeResult.Cluster.ComputeConfig.Enabled != nil && 
		   *describeResult.Cluster.ComputeConfig.Enabled {
			automodeClusters = append(automodeClusters, clusterName)
		}
	}
	
	return automodeClusters, nil
}
