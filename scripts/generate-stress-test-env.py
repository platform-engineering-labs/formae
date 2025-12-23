#!/usr/bin/env python3
# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

"""
Generate a large-scale AWS infrastructure environment for performance testing formae.

This script generates either PKL files (for formae) or CloudFormation templates
(for creating resources outside formae, useful for discovery testing).

The distribution of resource types mimics typical enterprise production environments.

Usage:
    # Generate PKL files for formae
    python3 scripts/generate-perf-test-env.py --count 1000 --region us-east-1
    python3 scripts/generate-perf-test-env.py --count 3000 --region eu-west-1 --output ./perf-test

    # Generate CloudFormation templates (for discovery testing)
    python3 scripts/generate-perf-test-env.py --format cloudformation --count 500 --region us-east-1

To apply PKL environment:
    cd <output-dir>
    pkl project resolve
    formae apply --mode reconcile main.pkl

To apply CloudFormation environment:
    cd <output-dir>
    ./deploy.sh                    # Deploy all stacks
    ./destroy.sh                   # Destroy all stacks

To destroy:
    formae destroy main.pkl
"""

import argparse
import math
import os
import sys
import uuid
from dataclasses import dataclass
from typing import List, Dict, Tuple


# Resource distribution percentages for a realistic production environment
# Based on typical enterprise AWS usage patterns
# NOTE: VPC-related resources are capped to stay within AWS quotas (max 5 VPCs, IGWs, NAT GWs)
DISTRIBUTION = {
    # Networking - Capped (8% total, down from 38%)
    # These scale logarithmically to stay within AWS quotas
    "vpc": 0.001,           # Cap at ~5 VPCs even for large environments
    "subnet": 0.01,         # Multiple subnets per VPC
    "route_table": 0.008,   # Route tables
    "route": 0.01,          # Routes
    "igw": 0.001,           # Internet gateways (1 per VPC, capped)
    "nat_gw": 0.003,        # NAT gateways (expensive, capped at ~5)
    "eip": 0.01,            # Elastic IPs (mostly for instances now)
    "security_group": 0.05, # Security groups
    "sg_ingress": 0.08,     # Security group rules
    "sg_egress": 0.03,      # Egress rules
    "vpc_endpoint": 0.01,   # VPC endpoints

    # Compute (15% - NEW)
    "ec2_instance": 0.08,   # EC2 instances
    "ebs_volume": 0.05,     # EBS volumes (some instances have extra volumes)
    "launch_template": 0.02, # Launch templates

    # IAM & Security (20%, down from 28%)
    "iam_role": 0.08,       # IAM roles
    "iam_policy": 0.05,     # IAM policies (managed + inline)
    "instance_profile": 0.03, # Instance profiles
    "kms_key": 0.01,        # KMS keys
    "secret": 0.02,         # Secrets
    "kms_alias": 0.01,      # KMS aliases

    # Storage (18%, up from 15%)
    "s3_bucket": 0.10,      # S3 buckets (very common)
    "dynamodb_table": 0.04, # DynamoDB tables
    "efs": 0.02,            # EFS file systems
    "efs_mount_target": 0.02, # EFS mount targets

    # Observability (12%, up from 10%)
    "log_group": 0.08,      # CloudWatch log groups
    "sqs_queue": 0.04,      # SQS queues

    # Application (12%, up from 9%)
    "lambda": 0.06,         # Lambda functions (very common)
    "ecr_repo": 0.03,       # ECR repositories

    # DNS & API Gateway (10% - NEW)
    "route53_zone": 0.02,   # Route53 hosted zones
    "route53_record": 0.05, # Route53 records
    "api_gateway": 0.02,    # API Gateway REST APIs
    "api_stage": 0.01,      # API Gateway stages

    # RDS (5% - NEW)
    "rds_instance": 0.02,   # RDS instances
    "db_subnet_group": 0.01, # DB subnet groups
    "db_parameter_group": 0.02, # DB parameter groups
}


@dataclass
class ResourceCounts:
    """Calculated resource counts based on total target."""
    vpcs: int
    subnets_per_vpc: int
    route_tables: int
    routes: int
    igws: int
    nat_gws: int
    eips: int
    security_groups: int
    sg_ingress_rules: int
    sg_egress_rules: int
    vpc_endpoints: int
    # Compute
    ec2_instances: int
    ebs_volumes: int
    launch_templates: int
    # IAM & Security
    iam_roles: int
    iam_policies: int
    instance_profiles: int
    kms_keys: int
    secrets: int
    kms_aliases: int
    # Storage
    s3_buckets: int
    dynamodb_tables: int
    efs_filesystems: int
    efs_mount_targets: int
    # Observability
    log_groups: int
    sqs_queues: int
    # Application
    lambdas: int
    ecr_repos: int
    # DNS & API Gateway
    route53_zones: int
    route53_records: int
    api_gateways: int
    api_stages: int
    # RDS
    rds_instances: int
    db_subnet_groups: int
    db_parameter_groups: int

    @property
    def total(self) -> int:
        return (
            self.vpcs +
            (self.vpcs * self.subnets_per_vpc) +
            self.route_tables +
            self.routes +
            self.igws +
            self.nat_gws +
            self.eips +
            self.security_groups +
            self.sg_ingress_rules +
            self.sg_egress_rules +
            self.vpc_endpoints +
            self.ec2_instances +
            self.ebs_volumes +
            self.launch_templates +
            self.iam_roles +
            self.iam_policies +
            self.instance_profiles +
            self.kms_keys +
            self.secrets +
            self.kms_aliases +
            self.s3_buckets +
            self.dynamodb_tables +
            self.efs_filesystems +
            self.efs_mount_targets +
            self.log_groups +
            self.sqs_queues +
            self.lambdas +
            self.ecr_repos +
            self.route53_zones +
            self.route53_records +
            self.api_gateways +
            self.api_stages +
            self.rds_instances +
            self.db_subnet_groups +
            self.db_parameter_groups +
            # Additional resources created implicitly
            self.vpcs +  # VPC gateway attachments
            (self.vpcs * self.subnets_per_vpc) +  # Subnet route table associations
            (self.ec2_instances * 2)  # Volume attachments (2 per instance on average)
        )


def calculate_counts(total: int) -> ResourceCounts:
    """Calculate resource counts based on target total and distribution.

    VPC-related resources are capped to stay within AWS quotas:
    - Max 5 VPCs (default quota)
    - Max 5 Internet Gateways (1 per VPC)
    - Max 5 NAT Gateways per AZ (we cap at 5 total)
    """
    # Cap VPCs using logarithmic scaling: 1 VPC for <500, 2 for <1000, 3 for <2000, max 5
    if total < 500:
        vpcs = 1
    elif total < 1000:
        vpcs = 2
    elif total < 2000:
        vpcs = 3
    elif total < 3500:
        vpcs = 4
    else:
        vpcs = 5  # Hard cap at 5 VPCs

    # Subnets per VPC: scale based on total but keep reasonable
    subnets_per_vpc = max(2, min(8, int(total * DISTRIBUTION["subnet"] / vpcs)))

    # Cap NAT gateways at 5 (expensive and quota-limited)
    nat_gws = min(5, max(1, int(total * DISTRIBUTION["nat_gw"])))

    return ResourceCounts(
        vpcs=vpcs,
        subnets_per_vpc=subnets_per_vpc,
        route_tables=max(vpcs * 2, int(total * DISTRIBUTION["route_table"])),
        routes=max(vpcs * 2, int(total * DISTRIBUTION["route"])),
        igws=vpcs,  # One per VPC, capped by VPC count
        nat_gws=nat_gws,
        eips=max(1, int(total * DISTRIBUTION["eip"])),
        security_groups=max(vpcs * 2, int(total * DISTRIBUTION["security_group"])),
        sg_ingress_rules=max(5, int(total * DISTRIBUTION["sg_ingress"])),
        sg_egress_rules=max(5, int(total * DISTRIBUTION["sg_egress"])),
        vpc_endpoints=max(1, int(total * DISTRIBUTION["vpc_endpoint"])),
        # Compute
        ec2_instances=max(2, int(total * DISTRIBUTION["ec2_instance"])),
        ebs_volumes=max(2, int(total * DISTRIBUTION["ebs_volume"])),
        launch_templates=max(1, int(total * DISTRIBUTION["launch_template"])),
        # IAM & Security
        iam_roles=max(5, int(total * DISTRIBUTION["iam_role"])),
        iam_policies=max(5, int(total * DISTRIBUTION["iam_policy"])),
        instance_profiles=max(2, int(total * DISTRIBUTION["instance_profile"])),
        kms_keys=max(1, int(total * DISTRIBUTION["kms_key"])),
        secrets=max(2, int(total * DISTRIBUTION["secret"])),
        kms_aliases=max(1, int(total * DISTRIBUTION["kms_alias"])),
        # Storage
        s3_buckets=max(5, int(total * DISTRIBUTION["s3_bucket"])),
        dynamodb_tables=max(2, int(total * DISTRIBUTION["dynamodb_table"])),
        efs_filesystems=max(1, int(total * DISTRIBUTION["efs"])),
        efs_mount_targets=max(2, int(total * DISTRIBUTION["efs_mount_target"])),
        # Observability
        log_groups=max(5, int(total * DISTRIBUTION["log_group"])),
        sqs_queues=max(2, int(total * DISTRIBUTION["sqs_queue"])),
        # Application
        lambdas=max(2, int(total * DISTRIBUTION["lambda"])),
        ecr_repos=max(2, int(total * DISTRIBUTION["ecr_repo"])),
        # DNS & API Gateway
        route53_zones=max(1, int(total * DISTRIBUTION["route53_zone"])),
        route53_records=max(5, int(total * DISTRIBUTION["route53_record"])),
        api_gateways=max(1, int(total * DISTRIBUTION["api_gateway"])),
        api_stages=max(1, int(total * DISTRIBUTION["api_stage"])),
        # RDS (note: db_subnet_groups require multiple AZs - disabled when vpcs < 2)
        rds_instances=0,  # Disabled: requires db_subnet_groups to work properly
        db_subnet_groups=0,  # Disabled: requires subnets in multiple AZs (need vpcs >= 2)
        db_parameter_groups=max(1, int(total * DISTRIBUTION["db_parameter_group"])),
    )


COPYRIGHT = '''/*
 * Performance Test Environment - Auto-generated
 * Generated by generate-perf-test-env.py
 *
 * WARNING: This environment is for testing only. Resources will incur AWS costs.
 */
'''


def generate_vars_pkl(env_id: str, region: str, stack_name: str) -> str:
    """Generate vars.pkl with environment configuration."""
    return f'''{COPYRIGHT}
import "@formae/formae.pkl"
import "@aws/aws.pkl"

envId = "{env_id}"
_region = "{region}"
stackName = "{stack_name}"

stack: formae.Stack = new {{
    label = stackName
    description = "Performance test environment {env_id}"
}}

target: formae.Target = new {{
    label = "perf-test-target"
    config = new aws.Config {{
        region = _region
    }}
}}

// Helper for unique naming
local function uniqueName(base: String): String = "\\(envId)-\\(base)"
'''


def generate_networking_pkl(counts: ResourceCounts, region: str) -> str:
    """Generate networking.pkl with VPCs, subnets, and routing."""

    # Generate subnet blocks dynamically for multiple AZs
    azs = ["a", "b", "c"]

    vpc_blocks = []
    for i in range(counts.vpcs):
        # Calculate CIDR blocks - use 10.{vpc_index}.0.0/16 pattern
        vpc_cidr = f"10.{i}.0.0/16"

        vpc_blocks.append(f'''
  hidden vpc{i}: vpc.VPC = new {{
    label = "\\(name)-vpc-{i}"
    cidrBlock = "{vpc_cidr}"
    enableDnsHostnames = true
    enableDnsSupport = true
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "Environment"; value = "perf-test" }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}

  hidden igw{i}: internetgateway.InternetGateway = new {{
    label = "\\(name)-igw-{i}"
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}

  hidden igwAttach{i}: vpcgatewayattachment.VPCGatewayAttachment = new {{
    label = "\\(name)-igw-attach-{i}"
    vpcId = vpc{i}.res.id
    internetGatewayId = igw{i}.res.id
  }}

  hidden publicRt{i}: routetable.RouteTable = new {{
    label = "\\(name)-public-rt-{i}"
    vpcId = vpc{i}.res.id
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}

  hidden privateRt{i}: routetable.RouteTable = new {{
    label = "\\(name)-private-rt-{i}"
    vpcId = vpc{i}.res.id
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}

  // Note: gatewayId references the attachment's InternetGatewayId to ensure
  // the route is created only after the IGW is attached to the VPC
  hidden publicRoute{i}: route.Route = new {{
    label = "\\(name)-public-route-{i}"
    routeTableId = publicRt{i}.res.id
    destinationCidrBlock = "0.0.0.0/0"
    gatewayId = igwAttach{i}.internetGatewayId
  }}''')

        # Generate subnets for this VPC
        for j in range(counts.subnets_per_vpc):
            az = azs[j % len(azs)]
            is_public = j < counts.subnets_per_vpc // 2
            subnet_type = "public" if is_public else "private"
            subnet_cidr = f"10.{i}.{j}.0/24"
            rt_ref = f"publicRt{i}" if is_public else f"privateRt{i}"

            vpc_blocks.append(f'''
  hidden subnet{i}_{j}: subnet.Subnet = new {{
    label = "\\(name)-{subnet_type}-subnet-{i}-{j}"
    vpcId = vpc{i}.res.id
    cidrBlock = "{subnet_cidr}"
    availabilityZone = "\\(region){az}"
    mapPublicIpOnLaunch = {str(is_public).lower()}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "Type"; value = "{subnet_type}" }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}

  hidden subnetRtAssoc{i}_{j}: subnetroutetableassociation.SubnetRouteTableAssociation = new {{
    label = "\\(name)-subnet-rt-assoc-{i}-{j}"
    subnetId = subnet{i}_{j}.res.id
    routeTableId = {rt_ref}.res.id
  }}''')

    # Generate resource listing
    resource_list = []
    for i in range(counts.vpcs):
        resource_list.append(f"    vpc{i}")
        resource_list.append(f"    igw{i}")
        resource_list.append(f"    igwAttach{i}")
        resource_list.append(f"    publicRt{i}")
        resource_list.append(f"    privateRt{i}")
        resource_list.append(f"    publicRoute{i}")
        for j in range(counts.subnets_per_vpc):
            resource_list.append(f"    subnet{i}_{j}")
            resource_list.append(f"    subnetRtAssoc{i}_{j}")

    return f'''{COPYRIGHT}
import "@aws/ec2/vpc.pkl"
import "@aws/ec2/subnet.pkl"
import "@aws/ec2/internetgateway.pkl"
import "@aws/ec2/routetable.pkl"
import "@aws/ec2/route.pkl"
import "@aws/ec2/subnetroutetableassociation.pkl"
import "@aws/ec2/vpcgatewayattachment.pkl"

class Networking {{
  name: String
  region: String
{"".join(vpc_blocks)}

  // Expose VPCs and subnets for other modules
  vpcs: Listing = new {{
{chr(10).join(f"    vpc{i}" for i in range(counts.vpcs))}
  }}

  // First private subnet per VPC for EFS mount targets
  privateSubnets: Listing = new {{
{chr(10).join(f"    subnet{i}_{counts.subnets_per_vpc // 2}" for i in range(counts.vpcs))}
  }}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_security_groups_pkl(counts: ResourceCounts) -> str:
    """Generate security_groups.pkl with security groups and rules."""

    sg_blocks = []
    ingress_blocks = []
    egress_blocks = []

    # Distribute SGs across VPCs
    for i in range(counts.security_groups):
        vpc_idx = i % max(1, int(counts.security_groups * 0.005))  # Distribute across VPCs
        sg_blocks.append(f'''
  hidden sg{i}: securitygroup.SecurityGroup = new {{
    label = "\\(name)-sg-{i}"
    groupDescription = "Security group {i} for perf test"
    vpcId = networking.vpcs.toList()[{vpc_idx}].res.id
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate ingress rules distributed across security groups
    for i in range(counts.sg_ingress_rules):
        sg_idx = i % counts.security_groups
        port = 80 + (i % 20) * 100  # Vary ports
        ingress_blocks.append(f'''
  hidden sgIngress{i}: securitygroupingress.SecurityGroupIngress = new {{
    label = "\\(name)-sg-ingress-{i}"
    groupId = sg{sg_idx}.res.groupId
    ipProtocol = "tcp"
    fromPort = {port}
    toPort = {port}
    cidrIp = "10.0.0.0/8"
    description = "Ingress rule {i}"
  }}''')

    # Generate egress rules
    for i in range(counts.sg_egress_rules):
        sg_idx = i % counts.security_groups
        egress_blocks.append(f'''
  hidden sgEgress{i}: securitygroupegress.SecurityGroupEgress = new {{
    label = "\\(name)-sg-egress-{i}"
    groupId = sg{sg_idx}.res.groupId
    ipProtocol = "-1"
    fromPort = -1
    toPort = -1
    cidrIp = "0.0.0.0/0"
  }}''')

    # Generate resource listing
    resource_list = []
    for i in range(counts.security_groups):
        resource_list.append(f"    sg{i}")
    for i in range(counts.sg_ingress_rules):
        resource_list.append(f"    sgIngress{i}")
    for i in range(counts.sg_egress_rules):
        resource_list.append(f"    sgEgress{i}")

    return f'''{COPYRIGHT}
import "@aws/ec2/securitygroup.pkl"
import "@aws/ec2/securitygroupingress.pkl"
import "@aws/ec2/securitygroupegress.pkl"

import "./networking.pkl" as networkingModule

class SecurityGroups {{
  name: String
  networking: networkingModule.Networking
{"".join(sg_blocks)}
{"".join(ingress_blocks)}
{"".join(egress_blocks)}

  // Expose security groups for other modules
  securityGroups: Listing = new {{
{chr(10).join(f"    sg{i}" for i in range(counts.security_groups))}
  }}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_compute_pkl(counts: ResourceCounts, region: str) -> str:
    """Generate compute.pkl with EC2 instances, volumes, and launch templates."""

    # Amazon Linux 2 AMIs by region (verified Dec 2024)
    ami_map = {
        "us-east-1": "ami-0c02fb55b2c6c5bdc",
        "us-east-2": "ami-06ba60a55dcbf8bf2",  # Verified working in us-east-2
        "us-west-1": "ami-0ecb7bb3a2c9a8b9d",
        "us-west-2": "ami-0c2ab3b8efb09f272",
        "eu-west-1": "ami-0c1c30571d2dae5c9",
        "eu-central-1": "ami-0a49b025fffbbdac6",
        "ap-southeast-1": "ami-0c802847a7dd848c0",
    }

    # Get AMI for the specified region, fallback to us-east-1
    ami_id = ami_map.get(region, ami_map["us-east-1"])

    lt_blocks = []
    instance_blocks = []
    volume_blocks = []
    volume_attachment_blocks = []

    # Generate launch templates
    for i in range(counts.launch_templates):
        lt_blocks.append(f'''
  hidden launchTemplate{i}: launchtemplate.LaunchTemplate = new {{
    label = "\\(name)-lt-{i}"
    launchTemplateName = "\\(name)-lt-{i}"
    launchTemplateData = new launchtemplate.LaunchTemplateData {{
      imageId = "{ami_id}"  // Amazon Linux 2023 - {region}
      instanceType = "t3.micro"
      monitoring = new launchtemplate.Monitoring {{
        enabled = true
      }}
      blockDeviceMappings {{
        new launchtemplate.BlockDeviceMapping {{
          deviceName = "/dev/xvda"
          ebs = new launchtemplate.EBS {{
            volumeSize = 8
            volumeType = "gp3"
            deleteOnTermination = true
          }}
        }}
      }}
    }}
    // Note: tagSpecifications removed - CloudControl API doesn't support "instance" resourceType for LaunchTemplate
  }}''')

    # Generate EC2 instances
    instance_types = ["t3.micro", "t3.small", "t3.medium"]
    for i in range(counts.ec2_instances):
        vpc_idx = i % counts.vpcs
        sg_idx = i % counts.security_groups
        instance_type = instance_types[i % len(instance_types)]

        instance_blocks.append(f'''
  hidden instance{i}: instance.Instance = new {{
    label = "\\(name)-instance-{i}"
    imageId = "{ami_id}"  // Amazon Linux 2023 - {region}
    instanceType = "{instance_type}"
    subnetId = networking.privateSubnets.toList()[{vpc_idx}].res.subnetId
    securityGroupIds {{
      sgs.securityGroups.toList()[{sg_idx}].res.groupId
    }}
    monitoring = true
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "Environment"; value = "perf-test" }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate EBS volumes (some standalone, some for attachment)
    # First 50% are standalone, rest are for attachment to instances
    standalone_volumes = counts.ebs_volumes // 2
    attachable_volumes = counts.ebs_volumes - standalone_volumes

    # Pre-calculate which instance each attachable volume will be attached to
    # This ensures we create volumes in the same AZ as their target instance
    num_attachments = min(attachable_volumes, counts.ec2_instances * 2)

    # Calculate the AZ of the private subnet that all instances use
    # privateSubnets contains subnet{vpc_idx}_{subnets_per_vpc // 2}
    # Subnet AZ is determined by the subnet index (j) within the VPC
    private_subnet_idx = counts.subnets_per_vpc // 2  # j value for private subnet
    private_subnet_az = ["a", "b", "c"][private_subnet_idx % 3]

    for i in range(counts.ebs_volumes):
        # For standalone volumes, distribute across AZs
        # For attachable volumes, use the same AZ as the private subnet (where instances run)
        if i < standalone_volumes:
            az_suffix = ["a", "b", "c"][i % 3]
        else:
            # This volume will be attached - use same AZ as the private subnet
            attachment_idx = i - standalone_volumes
            if attachment_idx < num_attachments:
                # All instances use the same private subnet, so all attachable volumes
                # need to be in that subnet's AZ
                az_suffix = private_subnet_az
            else:
                az_suffix = ["a", "b", "c"][i % 3]

        volume_blocks.append(f'''
  hidden volume{i}: volume.Volume = new {{
    label = "\\(name)-volume-{i}"
    availabilityZone = "\\(region){az_suffix}"
    size = {20 + (i % 5) * 10}  // 20-60 GB
    volumeType = "{["gp3", "gp2"][i % 2]}"  // Only use gp3/gp2 (io1 requires iops parameter)
    encrypted = {str(i % 2 == 0).lower()}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate volume attachments for the attachable volumes
    # Attach to instances (multiple volumes per instance)
    for i in range(num_attachments):
        instance_idx = i % counts.ec2_instances
        volume_idx = standalone_volumes + i
        device_names = ["/dev/sdf", "/dev/sdg", "/dev/sdh", "/dev/sdi"]
        device_name = device_names[i % len(device_names)]

        volume_attachment_blocks.append(f'''
  hidden volumeAttachment{i}: volumeattachment.VolumeAttachment = new {{
    label = "\\(name)-vol-attach-{i}"
    instanceId = instance{instance_idx}.res.instanceId
    volumeId = volume{volume_idx}.res.volumeId
    device = "{device_name}"
  }}''')

    # Generate resource listing
    resource_list = []
    for i in range(counts.launch_templates):
        resource_list.append(f"    launchTemplate{i}")
    for i in range(counts.ec2_instances):
        resource_list.append(f"    instance{i}")
    for i in range(counts.ebs_volumes):
        resource_list.append(f"    volume{i}")
    for i in range(len(volume_attachment_blocks)):
        resource_list.append(f"    volumeAttachment{i}")

    return f'''{COPYRIGHT}
import "@aws/ec2/launchtemplate.pkl"
import "@aws/ec2/instance.pkl"
import "@aws/ec2/volume.pkl"
import "@aws/ec2/volumeattachment.pkl"

import "./networking.pkl" as networkingModule
import "./security_groups.pkl" as securityGroupsModule

class Compute {{
  name: String
  region: String
  networking: networkingModule.Networking
  sgs: securityGroupsModule.SecurityGroups
{"".join(lt_blocks)}
{"".join(instance_blocks)}
{"".join(volume_blocks)}
{"".join(volume_attachment_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_iam_pkl(counts: ResourceCounts) -> str:
    """Generate iam.pkl with IAM roles, policies, and instance profiles."""

    role_blocks = []
    lambda_role_blocks = []
    policy_blocks = []
    profile_blocks = []

    # Calculate how many Lambda execution roles we need
    # Ensure we have enough Lambda roles for all Lambda functions (50% should be plenty)
    # But also ensure we have at least 1 non-Lambda role if we have policies or instance profiles
    min_other_roles = 1 if (counts.iam_policies > 0 or counts.instance_profiles > 0) else 0
    lambda_roles_count = min(
        max(5, int(counts.iam_roles * 0.2)),  # At least 5, or 20% of total roles
        counts.iam_roles - min_other_roles     # But leave room for non-Lambda roles if needed
    )
    other_roles_count = counts.iam_roles - lambda_roles_count

    # Generate Lambda execution roles with lambda.amazonaws.com trust policy
    for i in range(lambda_roles_count):
        lambda_role_blocks.append(f'''
  hidden lambdaRole{i}: role.Role = new {{
    label = "\\(name)-lambda-role-{i}"
    roleName = "\\(name)-lambda-role-{i}"
    assumeRolePolicyDocument {{
      ["Version"] = "2012-10-17"
      ["Statement"] {{
        new {{
          ["Effect"] = "Allow"
          ["Principal"] {{
            ["Service"] = "lambda.amazonaws.com"
          }}
          ["Action"] = "sts:AssumeRole"
        }}
      }}
    }}
    managedPolicyArns {{
      "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "Type"; value = "lambda-execution" }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate other IAM roles with various service principals
    services = ["ec2.amazonaws.com", "ecs-tasks.amazonaws.com",
                "sagemaker.amazonaws.com", "states.amazonaws.com"]

    for i in range(other_roles_count):
        service = services[i % len(services)]
        role_blocks.append(f'''
  hidden role{i}: role.Role = new {{
    label = "\\(name)-role-{i}"
    roleName = "\\(name)-role-{i}"
    assumeRolePolicyDocument {{
      ["Version"] = "2012-10-17"
      ["Statement"] {{
        new {{
          ["Effect"] = "Allow"
          ["Principal"] {{
            ["Service"] = "{service}"
          }}
          ["Action"] = "sts:AssumeRole"
        }}
      }}
    }}
    managedPolicyArns {{
      "arn:aws:iam::aws:policy/ReadOnlyAccess"
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate IAM policies (standalone - not attached to roles to avoid deletion order issues)
    # AWS requires policies to be detached before deletion, and roles to be removed from
    # instance profiles before deletion. By not using the `roles` property, we avoid
    # creating implicit attachments that cause deletion failures.
    for i in range(counts.iam_policies):
        policy_blocks.append(f'''
  hidden policy{i}: policy.ManagedPolicy = new {{
    label = "\\(name)-policy-{i}"
    managedPolicyName = "\\(name)-policy-{i}"
    policyDocument {{
      ["Version"] = "2012-10-17"
      ["Statement"] {{
        new {{
          ["Effect"] = "Allow"
          ["Action"] {{
            "logs:CreateLogGroup"
            "logs:CreateLogStream"
            "logs:PutLogEvents"
          }}
          ["Resource"] = "*"
        }}
      }}
    }}
  }}''')

    # Generate instance profiles (standalone - not attached to roles to avoid deletion order issues)
    # AWS requires roles to be removed from instance profiles before the role can be deleted.
    # By not using the `roles` property, we avoid creating implicit attachments.
    for i in range(counts.instance_profiles):
        profile_blocks.append(f'''
  hidden instanceProfile{i}: instanceprofile.InstanceProfile = new {{
    label = "\\(name)-instance-profile-{i}"
    instanceProfileName = "\\(name)-instance-profile-{i}"
  }}''')

    # Generate resource listing
    resource_list = []
    for i in range(lambda_roles_count):
        resource_list.append(f"    lambdaRole{i}")
    for i in range(other_roles_count):
        resource_list.append(f"    role{i}")
    for i in range(counts.iam_policies):
        resource_list.append(f"    policy{i}")
    for i in range(counts.instance_profiles):
        resource_list.append(f"    instanceProfile{i}")

    return f'''{COPYRIGHT}
import "@aws/iam/role.pkl"
import "@aws/iam/managedpolicy.pkl" as policy
import "@aws/iam/instanceprofile.pkl"

class IAM {{
  name: String
{"".join(lambda_role_blocks)}
{"".join(role_blocks)}
{"".join(policy_blocks)}
{"".join(profile_blocks)}

  // Expose Lambda execution roles separately for Lambda functions
  lambdaRoles: Listing = new {{
{chr(10).join(f"    lambdaRole{i}" for i in range(lambda_roles_count))}
  }}

  // Expose other roles for general use
  roles: Listing = new {{
{chr(10).join(f"    role{i}" for i in range(other_roles_count))}
  }}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_kms_pkl(counts: ResourceCounts) -> str:
    """Generate kms.pkl with KMS keys and aliases."""

    key_blocks = []
    alias_blocks = []

    for i in range(counts.kms_keys):
        key_blocks.append(f'''
  hidden kmsKey{i}: key.Key = new {{
    label = "\\(name)-kms-key-{i}"
    description = "KMS key {i} for perf test"
    enableKeyRotation = true
    keyPolicy {{
      ["Version"] = "2012-10-17"
      ["Statement"] {{
        new {{
          ["Sid"] = "Enable IAM User Permissions"
          ["Effect"] = "Allow"
          ["Principal"] {{
            ["AWS"] = "*"
          }}
          ["Action"] = "kms:*"
          ["Resource"] = "*"
        }}
      }}
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    for i in range(min(counts.kms_aliases, counts.kms_keys)):
        alias_blocks.append(f'''
  hidden kmsAlias{i}: alias.Alias = new {{
    label = "\\(name)-kms-alias-{i}"
    aliasName = "alias/\\(name)-key-{i}"
    targetKeyId = kmsKey{i}.res.arn
  }}''')

    resource_list = []
    for i in range(counts.kms_keys):
        resource_list.append(f"    kmsKey{i}")
    for i in range(min(counts.kms_aliases, counts.kms_keys)):
        resource_list.append(f"    kmsAlias{i}")

    return f'''{COPYRIGHT}
import "@aws/kms/key.pkl"
import "@aws/kms/kmsalias.pkl" as alias

class KMS {{
  name: String
{"".join(key_blocks)}
{"".join(alias_blocks)}

  // Expose keys for other modules
  keys: Listing = new {{
{chr(10).join(f"    kmsKey{i}" for i in range(counts.kms_keys))}
  }}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_storage_pkl(counts: ResourceCounts) -> str:
    """Generate storage.pkl with S3 buckets, DynamoDB tables, and EFS."""

    bucket_blocks = []
    dynamo_blocks = []
    efs_blocks = []
    mount_target_blocks = []

    # S3 Buckets
    for i in range(counts.s3_buckets):
        bucket_blocks.append(f'''
  hidden bucket{i}: bucket.Bucket = new {{
    label = "\\(name)-bucket-{i}"
    // S3 bucket names must be globally unique - include full env name
    bucketName = "\\(name)-bucket-{i}"
    versioningConfiguration = new bucket.VersioningConfiguration {{
      status = "Enabled"
    }}
    publicAccessBlockConfiguration = new bucket.PublicAccessBlockConfiguration {{
      blockPublicAcls = true
      blockPublicPolicy = true
      ignorePublicAcls = true
      restrictPublicBuckets = true
    }}
    bucketEncryption = new bucket.BucketEncryption {{
      serverSideEncryptionConfiguration {{
        new bucket.ServerSideEncryptionRule {{
          serverSideEncryptionByDefault = new bucket.ServerSideEncryptionByDefault {{
            sseAlgorithm = "AES256"
          }}
        }}
      }}
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # DynamoDB Tables
    for i in range(counts.dynamodb_tables):
        dynamo_blocks.append(f'''
  hidden dynamoTable{i}: table.Table = new {{
    label = "\\(name)-table-{i}"
    tableName = "\\(name)-table-{i}"
    billingMode = "PAY_PER_REQUEST"
    attributeDefinitions {{
      new table.AttributeDefinition {{
        attributeName = "pk"
        attributeType = "S"
      }}
      new table.AttributeDefinition {{
        attributeName = "sk"
        attributeType = "S"
      }}
    }}
    keySchema {{
      new table.KeySchema {{
        attributeName = "pk"
        keyType = "HASH"
      }}
      new table.KeySchema {{
        attributeName = "sk"
        keyType = "RANGE"
      }}
    }}
    deletionProtectionEnabled = false
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # EFS File Systems
    for i in range(counts.efs_filesystems):
        efs_blocks.append(f'''
  hidden efs{i}: filesystem.FileSystem = new {{
    label = "\\(name)-efs-{i}"
    performanceMode = "generalPurpose"
    encrypted = true
    fileSystemTags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # EFS Mount Targets (distributed across subnets)
    for i in range(min(counts.efs_mount_targets, counts.efs_filesystems)):
        mount_target_blocks.append(f'''
  hidden efsMountTarget{i}: mounttarget.MountTarget = new {{
    label = "\\(name)-efs-mount-{i}"
    fileSystemId = efs{i}.res.fileSystemId
    subnetId = networking.privateSubnets.toList()[{i % max(1, int(counts.efs_filesystems * 0.005))}].res.id
    securityGroups {{
      sgs.securityGroups.toList()[0].res.groupId
    }}
  }}''')

    resource_list = []
    for i in range(counts.s3_buckets):
        resource_list.append(f"    bucket{i}")
    for i in range(counts.dynamodb_tables):
        resource_list.append(f"    dynamoTable{i}")
    for i in range(counts.efs_filesystems):
        resource_list.append(f"    efs{i}")
    for i in range(min(counts.efs_mount_targets, counts.efs_filesystems)):
        resource_list.append(f"    efsMountTarget{i}")

    return f'''{COPYRIGHT}
import "@aws/s3/bucket.pkl"
import "@aws/dynamodb/table.pkl"
import "@aws/efs/filesystem.pkl"
import "@aws/efs/mounttarget.pkl"

import "./networking.pkl" as networkingModule
import "./security_groups.pkl" as securityGroupsModule

class Storage {{
  name: String
  networking: networkingModule.Networking
  sgs: securityGroupsModule.SecurityGroups
{"".join(bucket_blocks)}
{"".join(dynamo_blocks)}
{"".join(efs_blocks)}
{"".join(mount_target_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_observability_pkl(counts: ResourceCounts) -> str:
    """Generate observability.pkl with CloudWatch Log Groups and SQS Queues."""

    log_group_blocks = []
    sqs_blocks = []

    # CloudWatch Log Groups
    for i in range(counts.log_groups):
        log_group_blocks.append(f'''
  hidden logGroup{i}: loggroup.LogGroup = new {{
    label = "\\(name)-log-group-{i}"
    logGroupName = "/perf-test/\\(name)/app-{i}"
    retentionInDays = 7
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # SQS Queues (SNS not available in the AWS plugin)
    for i in range(counts.sqs_queues):
        sqs_blocks.append(f'''
  hidden sqsQueue{i}: queue.Queue = new {{
    label = "\\(name)-sqs-queue-{i}"
    queueName = "\\(name)-queue-{i}"
    visibilityTimeout = 30.s
    messageRetentionPeriod = 345600.s
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.log_groups):
        resource_list.append(f"    logGroup{i}")
    for i in range(counts.sqs_queues):
        resource_list.append(f"    sqsQueue{i}")

    return f'''{COPYRIGHT}
import "@aws/logs/loggroup.pkl"
import "@aws/sqs/queue.pkl"

class Observability {{
  name: String
{"".join(log_group_blocks)}
{"".join(sqs_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_secrets_pkl(counts: ResourceCounts) -> str:
    """Generate secrets.pkl with Secrets Manager secrets."""

    secret_blocks = []

    # Secrets Manager secrets (SSM not available in the AWS plugin)
    for i in range(counts.secrets):
        secret_blocks.append(f'''
  hidden secret{i}: secret.Secret = new {{
    label = "\\(envName)-secret-{i}"
    name = "\\(envName)/secret-{i}"
    description = "Secret {i} for perf test"
    generateSecretString = new secret.GenerateSecretString {{
      secretStringTemplate = #"{{"username": "admin-{i}"}}"#
      generateStringKey = "password"
      passwordLength = 32
      excludePunctuation = true
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.secrets):
        resource_list.append(f"    secret{i}")

    return f'''{COPYRIGHT}
import "@aws/secretsmanager/secret.pkl"

class Secrets {{
  envName: String
{"".join(secret_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_application_pkl(counts: ResourceCounts) -> str:
    """Generate application.pkl with Lambda functions and ECR repos."""

    lambda_blocks = []
    ecr_blocks = []

    # Calculate lambda_roles_count the same way as in generate_iam_pkl
    min_other_roles = 1 if (counts.iam_policies > 0 or counts.instance_profiles > 0) else 0
    lambda_roles_count = min(
        max(5, int(counts.iam_roles * 0.2)),
        counts.iam_roles - min_other_roles
    )

    # Lambda functions (without actual code - just the function resource)
    # Use the dedicated Lambda execution roles pool
    for i in range(counts.lambdas):
        role_idx = i % lambda_roles_count
        lambda_blocks.append(f'''
  hidden lambda{i}: lambdaFunc.Function = new {{
    label = "\\(name)-lambda-{i}"
    functionName = "\\(name)-lambda-{i}"
    runtime = "python3.11"
    handler = "index.handler"
    role = iam.lambdaRoles.toList()[{role_idx}].res.arn
    // Minimal inline code - just a placeholder
    code = new lambdaFunc.Code {{
      zipFile = #"def handler(event, context): return {{'statusCode': 200}}"#
    }}
    memorySize = 128
    timeout = 30
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # ECR Repositories
    for i in range(counts.ecr_repos):
        ecr_blocks.append(f'''
  hidden ecrRepo{i}: repository.Repository = new {{
    label = "\\(name)-ecr-repo-{i}"
    repositoryName = "\\(name)-repo-{i}"
    imageScanningConfiguration = new repository.ImageScanningConfiguration {{
      scanOnPush = true
    }}
    imageTagMutability = "MUTABLE"
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.lambdas):
        resource_list.append(f"    lambda{i}")
    for i in range(counts.ecr_repos):
        resource_list.append(f"    ecrRepo{i}")

    return f'''{COPYRIGHT}
import "@aws/lambda/func.pkl" as lambdaFunc
import "@aws/ecr/repository.pkl"

import "./iam.pkl" as iamModule

class Application {{
  name: String
  iam: iamModule.IAM
{"".join(lambda_blocks)}
{"".join(ecr_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_route53_pkl(counts: ResourceCounts) -> str:
    """Generate route53.pkl with hosted zones and DNS records."""

    zone_blocks = []
    record_blocks = []

    # Generate hosted zones
    for i in range(counts.route53_zones):
        zone_blocks.append(f'''
  hidden hostedZone{i}: hostedzone.HostedZone = new {{
    label = "\\(envName)-zone-{i}"
    name = "\\(envName)-zone-{i}.perftest.internal"  // Use .internal to avoid AWS reserved domains
    hostedZoneConfig = new hostedzone.HostedZoneConfig {{
      comment = "Hosted zone {i} for perf test"
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate DNS records distributed across zones
    record_types = ["A", "CNAME", "TXT"]
    for i in range(counts.route53_records):
        zone_idx = i % counts.route53_zones
        record_type = record_types[i % len(record_types)]

        if record_type == "A":
            resource_records = '"1.2.3.4"\n      "5.6.7.8"'
        elif record_type == "CNAME":
            resource_records = '"target.perftest.internal"'
        else:  # TXT
            resource_records = '"\\"v=spf1 -all\\""'

        record_blocks.append(f'''
  hidden record{i}: recordset.RecordSet = new {{
    label = "\\(envName)-record-{i}"
    hostedZoneId = hostedZone{zone_idx}.res.id
    name = "record-{i}.\\(envName)-zone-{zone_idx}.perftest.internal"
    type = "{record_type}"
    ttl = 300
    resourceRecords {{
      {resource_records}
    }}
  }}''')

    resource_list = []
    for i in range(counts.route53_zones):
        resource_list.append(f"    hostedZone{i}")
    for i in range(counts.route53_records):
        resource_list.append(f"    record{i}")

    return f'''{COPYRIGHT}
import "@aws/route53/hostedzone.pkl"
import "@aws/route53/recordset.pkl"

class Route53 {{
  envName: String
{"".join(zone_blocks)}
{"".join(record_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_api_gateway_pkl(counts: ResourceCounts) -> str:
    """Generate api_gateway.pkl with REST APIs.

    Note: Deployments and Stages are not included because AWS requires at least one
    Method to exist on the REST API before creating a Deployment. Adding Methods
    would require additional Resource configuration.
    """

    api_blocks = []

    # Generate REST APIs only (no Deployments/Stages - requires Methods first)
    for i in range(counts.api_gateways):
        api_blocks.append(f'''
  hidden restApi{i}: restapi.RestApi = new {{
    label = "\\(envName)-api-{i}"
    name = "\\(envName)-api-{i}"
    description = "API Gateway {i} for perf test"
    endpointConfiguration = new restapi.EndpointConfiguration {{
      types {{
        "REGIONAL"
      }}
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.api_gateways):
        resource_list.append(f"    restApi{i}")

    return f'''{COPYRIGHT}
import "@aws/apigateway/restapi.pkl"

class ApiGateway {{
  envName: String
{"".join(api_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_rds_pkl(counts: ResourceCounts) -> str:
    """Generate rds.pkl with RDS instances, subnet groups, and parameter groups."""

    subnet_group_blocks = []
    param_group_blocks = []
    instance_blocks = []

    # Generate DB subnet groups
    for i in range(counts.db_subnet_groups):
        # Use private subnets from different VPCs
        vpc_idx = i % counts.vpcs
        subnet_group_blocks.append(f'''
  hidden dbSubnetGroup{i}: dbsubnetgroup.DBSubnetGroup = new {{
    label = "\\(name)-db-subnet-group-{i}"
    dbSubnetGroupName = "\\(name)-db-subnet-group-{i}"
    dbSubnetGroupDescription = "DB subnet group {i} for perf test"
    subnetIds {{
      networking.privateSubnets.toList()[{vpc_idx}].res.id
      networking.privateSubnets.toList()[{(vpc_idx + 1) % max(1, counts.vpcs)}].res.id
    }}
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate DB parameter groups
    engines = ["mysql8.0", "postgres14", "mariadb10.6"]
    for i in range(counts.db_parameter_groups):
        engine_family = engines[i % len(engines)]
        param_group_blocks.append(f'''
  hidden dbParamGroup{i}: dbparametergroup.DBParameterGroup = new {{
    label = "\\(name)-db-param-group-{i}"
    dbParameterGroupName = "\\(name)-db-param-group-{i}"
    description = "DB parameter group {i} for perf test"
    family = "{engine_family}"
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Generate RDS instances
    for i in range(counts.rds_instances):
        subnet_group_idx = i % counts.db_subnet_groups
        param_group_idx = i % counts.db_parameter_groups
        sg_idx = i % counts.security_groups
        engine = ["mysql", "postgres", "mariadb"][i % 3]
        engine_version = ["8.0.35", "14.9", "10.6.14"][i % 3]
        instance_class = ["db.t3.micro", "db.t3.small"][i % 2]

        instance_blocks.append(f'''
  hidden rdsInstance{i}: dbinstance.DBInstance = new {{
    label = "\\(name)-rds-{i}"
    dbInstanceIdentifier = "\\(name)-rds-{i}"
    dbInstanceClass = "{instance_class}"
    engine = "{engine}"
    engineVersion = "{engine_version}"
    masterUsername = "admin"
    masterUserPassword = "TempPassword123!"  // In real env, use Secrets Manager
    allocatedStorage = 20
    dbSubnetGroupName = dbSubnetGroup{subnet_group_idx}.res.dbSubnetGroupName
    // Use string literal since dbParameterGroupName doesn't accept Resolvable
    dbParameterGroupName = "\\(name)-db-param-group-{param_group_idx}"
    vpcSecurityGroups {{
      sgs.securityGroups.toList()[{sg_idx}].res.groupId
    }}
    publiclyAccessible = false
    storageEncrypted = true
    backupRetentionPeriod = 7
    deletionProtection = false
    tags {{
      new {{ key = "Name"; value = label }}
      new {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.db_subnet_groups):
        resource_list.append(f"    dbSubnetGroup{i}")
    for i in range(counts.db_parameter_groups):
        resource_list.append(f"    dbParamGroup{i}")
    for i in range(counts.rds_instances):
        resource_list.append(f"    rdsInstance{i}")

    return f'''{COPYRIGHT}
import "@aws/rds/dbsubnetgroup.pkl"
import "@aws/rds/dbparametergroup.pkl"
import "@aws/rds/dbinstance.pkl"

import "./networking.pkl" as networkingModule
import "./security_groups.pkl" as securityGroupsModule

class RDS {{
  name: String
  networking: networkingModule.Networking
  sgs: securityGroupsModule.SecurityGroups
{"".join(subnet_group_blocks)}
{"".join(param_group_blocks)}
{"".join(instance_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_main_pkl(env_id: str, stack_name: str, counts: ResourceCounts) -> str:
    """Generate main.pkl that ties everything together."""

    return f'''{COPYRIGHT}
amends "@formae/forma.pkl"
import "@formae/formae.pkl"

import "./vars.pkl"
import "./networking.pkl" as networkingModule
import "./security_groups.pkl" as securityGroupsModule
import "./compute.pkl" as computeModule
import "./iam.pkl" as iamModule
import "./kms.pkl" as kmsModule
import "./storage.pkl" as storageModule
import "./observability.pkl" as observabilityModule
import "./secrets.pkl" as secretsModule
import "./application.pkl" as applicationModule
import "./route53.pkl" as route53Module
import "./api_gateway.pkl" as apiGatewayModule
import "./rds.pkl" as rdsModule

description {{
    text = """
    Performance Test Environment: {env_id}

    This environment contains approximately {counts.total} AWS resources
    distributed across various resource types to simulate a realistic
    production environment.

    Resource Distribution:
    - Networking: VPCs (capped at 5), Subnets, Route Tables, Security Groups
    - Compute: EC2 Instances, EBS Volumes, Launch Templates
    - IAM: Roles, Policies, Instance Profiles
    - Storage: S3 Buckets, DynamoDB Tables, EFS
    - Observability: CloudWatch Log Groups, SQS Queues
    - Application: Lambda Functions, ECR Repositories
    - DNS & API: Route53 Hosted Zones/Records, API Gateway
    - Database: RDS Instances, DB Subnet Groups, DB Parameter Groups
    - Secrets: Secrets Manager
    - Security: KMS Keys

    WARNING: This will create real AWS resources that incur costs.
    Use 'formae destroy --stack {stack_name}' to clean up.
    """
    confirm = true
}}

local _name = vars.envId
local _region = vars._region

local _networking = new networkingModule.Networking {{
    name = _name
    region = _region
}}

local _securityGroups = new securityGroupsModule.SecurityGroups {{
    name = _name
    networking = _networking
}}

local _compute = new computeModule.Compute {{
    name = _name
    region = _region
    networking = _networking
    sgs = _securityGroups
}}

local _iam = new iamModule.IAM {{
    name = _name
}}

local _kms = new kmsModule.KMS {{
    name = _name
}}

local _storage = new storageModule.Storage {{
    name = _name
    networking = _networking
    sgs = _securityGroups
}}

local _observability = new observabilityModule.Observability {{
    name = _name
}}

local _secrets = new secretsModule.Secrets {{
    envName = _name
}}

local _application = new applicationModule.Application {{
    name = _name
    iam = _iam
}}

local _route53 = new route53Module.Route53 {{
    envName = _name
}}

local _apiGateway = new apiGatewayModule.ApiGateway {{
    envName = _name
}}

local _rds = new rdsModule.RDS {{
    name = _name
    networking = _networking
    sgs = _securityGroups
}}

forma {{
    vars.stack
    vars.target

    ..._networking.resources
    ..._securityGroups.resources
    ..._compute.resources
    ..._iam.resources
    ..._kms.resources
    ..._storage.resources
    ..._observability.resources
    ..._secrets.resources
    ..._application.resources
    ..._route53.resources
    ..._apiGateway.resources
    ..._rds.resources
}}
'''


def generate_pkl_project(formae_path: str) -> str:
    """Generate PklProject file."""
    return f'''amends "pkl:Project"

dependencies {{
  ["formae"] = import("{formae_path}/plugins/pkl/schema/PklProject")
  ["aws"] = import("{formae_path}/plugins/aws/schema/pkl/PklProject")
}}
'''


def generate_readme(env_id: str, stack_name: str, region: str, counts: ResourceCounts) -> str:
    """Generate README.md with usage instructions."""
    return f'''# Performance Test Environment: {env_id}

## Overview

This directory contains PKL files that define a large-scale AWS infrastructure
environment for performance testing formae. The environment creates approximately
**{counts.total} resources** distributed across various AWS services.

## Resource Distribution

| Category | Resources | Count |
|----------|-----------|-------|
| Networking | VPCs (capped at 5), Subnets, Route Tables, IGWs, NAT GWs | {counts.vpcs + counts.vpcs * counts.subnets_per_vpc + counts.route_tables + counts.igws + counts.nat_gws} |
| Security Groups | SGs + Rules | {counts.security_groups + counts.sg_ingress_rules + counts.sg_egress_rules} |
| Compute | EC2 Instances, EBS Volumes, Launch Templates | {counts.ec2_instances + counts.ebs_volumes + counts.launch_templates} |
| IAM | Roles, Policies, Profiles | {counts.iam_roles + counts.iam_policies + counts.instance_profiles} |
| Storage | S3, DynamoDB, EFS | {counts.s3_buckets + counts.dynamodb_tables + counts.efs_filesystems + counts.efs_mount_targets} |
| Observability | Logs, SQS | {counts.log_groups + counts.sqs_queues} |
| Application | Lambda, ECR | {counts.lambdas + counts.ecr_repos} |
| DNS & API | Route53, API Gateway | {counts.route53_zones + counts.route53_records + counts.api_gateways + counts.api_stages} |
| RDS | Instances, Subnet Groups, Parameter Groups | {counts.rds_instances + counts.db_subnet_groups + counts.db_parameter_groups} |
| Secrets | Secrets Manager | {counts.secrets} |
| KMS | Keys, Aliases | {counts.kms_keys + counts.kms_aliases} |

## Configuration

- **Environment ID**: `{env_id}`
- **Stack Name**: `{stack_name}`
- **Region**: `{region}`

## Usage

### Prerequisites

1. AWS credentials configured
2. formae agent running: `formae agent start`
3. PKL CLI installed

### Deploy

```bash
# Resolve PKL dependencies
pkl project resolve

# Preview the deployment (dry-run)
formae apply --simulate main.pkl

# Deploy the environment
formae apply main.pkl
```

### Monitor Progress

```bash
# Check deployment status
formae status

# View resources
formae inventory
```

### Cleanup

```bash
# Destroy all resources
formae destroy --stack {stack_name}
```

## Cost Warning

This environment creates real AWS resources that will incur costs:
- NAT Gateways (~$0.045/hour each)
- KMS Keys (~$1/month each)
- Secrets Manager (~$0.40/secret/month)
- CloudWatch Logs (varies by ingestion)

**Always destroy the environment when testing is complete!**

## Files

- `main.pkl` - Main entry point
- `vars.pkl` - Environment configuration
- `networking.pkl` - VPCs, Subnets, Routing
- `security_groups.pkl` - Security Groups and Rules
- `iam.pkl` - IAM Roles, Policies, Instance Profiles
- `kms.pkl` - KMS Keys and Aliases
- `storage.pkl` - S3, DynamoDB, EFS
- `observability.pkl` - CloudWatch, SNS, SQS
- `secrets.pkl` - Secrets Manager, SSM Parameters
- `application.pkl` - Lambda, ECR
'''


# ============================================================================
# CloudFormation Generators
# ============================================================================

CFN_HEADER = """AWSTemplateFormatVersion: '2010-09-09'
Description: '{description}'

"""


def cfn_tags(name: str, extra: dict = None) -> str:
    """Generate CloudFormation tags block."""
    tags = [
        f"        - Key: Name\n          Value: {name}",
        "        - Key: Environment\n          Value: perf-test",
        "        - Key: ManagedBy\n          Value: cloudformation",
    ]
    if extra:
        for k, v in extra.items():
            tags.append(f"        - Key: {k}\n          Value: {v}")
    return "\n".join(tags)


def generate_cfn_networking(counts: ResourceCounts, env_id: str, region: str) -> str:
    """Generate CloudFormation template for networking resources."""
    azs = ["a", "b", "c"]

    template = CFN_HEADER.format(description=f"Networking stack for perf test {env_id}")
    template += "Resources:\n"

    # Track outputs for cross-stack references
    outputs = []

    # VPCs
    for i in range(counts.vpcs):
        vpc_cidr = f"10.{i}.0.0/16"
        template += f"""
  VPC{i}:
    Type: AWS::EC2::VPC
    Properties:
      CidrBlock: {vpc_cidr}
      EnableDnsHostnames: true
      EnableDnsSupport: true
      Tags:
{cfn_tags(f'{env_id}-vpc-{i}')}

  IGW{i}:
    Type: AWS::EC2::InternetGateway
    Properties:
      Tags:
{cfn_tags(f'{env_id}-igw-{i}')}

  IGWAttachment{i}:
    Type: AWS::EC2::VPCGatewayAttachment
    Properties:
      VpcId: !Ref VPC{i}
      InternetGatewayId: !Ref IGW{i}

  PublicRouteTable{i}:
    Type: AWS::EC2::RouteTable
    Properties:
      VpcId: !Ref VPC{i}
      Tags:
{cfn_tags(f'{env_id}-public-rt-{i}')}

  PrivateRouteTable{i}:
    Type: AWS::EC2::RouteTable
    Properties:
      VpcId: !Ref VPC{i}
      Tags:
{cfn_tags(f'{env_id}-private-rt-{i}')}

  PublicRoute{i}:
    Type: AWS::EC2::Route
    DependsOn: IGWAttachment{i}
    Properties:
      RouteTableId: !Ref PublicRouteTable{i}
      DestinationCidrBlock: 0.0.0.0/0
      GatewayId: !Ref IGW{i}
"""
        outputs.append(f"  VPC{i}Id:\n    Value: !Ref VPC{i}\n    Export:\n      Name: !Sub '${{AWS::StackName}}-VPC{i}Id'")
        outputs.append(f"  PublicRouteTable{i}Id:\n    Value: !Ref PublicRouteTable{i}\n    Export:\n      Name: !Sub '${{AWS::StackName}}-PublicRT{i}Id'")
        outputs.append(f"  PrivateRouteTable{i}Id:\n    Value: !Ref PrivateRouteTable{i}\n    Export:\n      Name: !Sub '${{AWS::StackName}}-PrivateRT{i}Id'")

        # Subnets for this VPC
        for j in range(counts.subnets_per_vpc):
            az = azs[j % len(azs)]
            is_public = j < counts.subnets_per_vpc // 2
            subnet_type = "public" if is_public else "private"
            subnet_cidr = f"10.{i}.{j}.0/24"
            rt_ref = f"PublicRouteTable{i}" if is_public else f"PrivateRouteTable{i}"

            template += f"""
  Subnet{i}x{j}:
    Type: AWS::EC2::Subnet
    Properties:
      VpcId: !Ref VPC{i}
      CidrBlock: {subnet_cidr}
      AvailabilityZone: {region}{az}
      MapPublicIpOnLaunch: {str(is_public).lower()}
      Tags:
{cfn_tags(f'{env_id}-{subnet_type}-subnet-{i}-{j}', {'Type': subnet_type})}

  SubnetRTAssoc{i}x{j}:
    Type: AWS::EC2::SubnetRouteTableAssociation
    Properties:
      SubnetId: !Ref Subnet{i}x{j}
      RouteTableId: !Ref {rt_ref}
"""
            outputs.append(f"  Subnet{i}x{j}Id:\n    Value: !Ref Subnet{i}x{j}\n    Export:\n      Name: !Sub '${{AWS::StackName}}-Subnet{i}x{j}Id'")

    # Add outputs section
    if outputs:
        template += "\nOutputs:\n" + "\n".join(outputs)

    return template


def generate_cfn_security_groups(counts: ResourceCounts, env_id: str, networking_stack: str) -> str:
    """Generate CloudFormation template for security groups."""
    template = CFN_HEADER.format(description=f"Security groups stack for perf test {env_id}")
    template += "Resources:\n"

    outputs = []

    # Security groups distributed across VPCs
    for i in range(counts.security_groups):
        vpc_idx = i % counts.vpcs
        template += f"""
  SG{i}:
    Type: AWS::EC2::SecurityGroup
    Properties:
      GroupDescription: Security group {i} for perf test
      VpcId: !ImportValue {networking_stack}-VPC{vpc_idx}Id
      Tags:
{cfn_tags(f'{env_id}-sg-{i}')}
"""
        outputs.append(f"  SG{i}Id:\n    Value: !GetAtt SG{i}.GroupId\n    Export:\n      Name: !Sub '${{AWS::StackName}}-SG{i}Id'")

    # Ingress rules
    for i in range(counts.sg_ingress_rules):
        sg_idx = i % counts.security_groups
        port = 80 + (i % 20) * 100
        template += f"""
  SGIngress{i}:
    Type: AWS::EC2::SecurityGroupIngress
    Properties:
      GroupId: !GetAtt SG{sg_idx}.GroupId
      IpProtocol: tcp
      FromPort: {port}
      ToPort: {port}
      CidrIp: 10.0.0.0/8
      Description: Ingress rule {i}
"""

    # Egress rules
    for i in range(counts.sg_egress_rules):
        sg_idx = i % counts.security_groups
        template += f"""
  SGEgress{i}:
    Type: AWS::EC2::SecurityGroupEgress
    Properties:
      GroupId: !GetAtt SG{sg_idx}.GroupId
      IpProtocol: '-1'
      CidrIp: 0.0.0.0/0
"""

    if outputs:
        template += "\nOutputs:\n" + "\n".join(outputs)

    return template


def generate_cfn_iam(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for IAM resources."""
    template = CFN_HEADER.format(description=f"IAM stack for perf test {env_id}")
    template += "Resources:\n"

    outputs = []
    services = ["ec2.amazonaws.com", "ecs-tasks.amazonaws.com", "sagemaker.amazonaws.com", "states.amazonaws.com"]

    # Calculate Lambda roles count same as PKL
    min_other_roles = 1 if (counts.iam_policies > 0 or counts.instance_profiles > 0) else 0
    lambda_roles_count = min(
        max(5, int(counts.iam_roles * 0.2)),
        counts.iam_roles - min_other_roles
    )
    other_roles_count = counts.iam_roles - lambda_roles_count

    # Lambda execution roles
    for i in range(lambda_roles_count):
        template += f"""
  LambdaRole{i}:
    Type: AWS::IAM::Role
    Properties:
      RoleName: {env_id}-lambda-role-{i}
      AssumeRolePolicyDocument:
        Version: '2012-10-17'
        Statement:
          - Effect: Allow
            Principal:
              Service: lambda.amazonaws.com
            Action: sts:AssumeRole
      ManagedPolicyArns:
        - arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole
      Tags:
{cfn_tags(f'{env_id}-lambda-role-{i}', {'Type': 'lambda-execution'})}
"""
        outputs.append(f"  LambdaRole{i}Arn:\n    Value: !GetAtt LambdaRole{i}.Arn\n    Export:\n      Name: !Sub '${{AWS::StackName}}-LambdaRole{i}Arn'")

    # Other IAM roles
    for i in range(other_roles_count):
        service = services[i % len(services)]
        template += f"""
  Role{i}:
    Type: AWS::IAM::Role
    Properties:
      RoleName: {env_id}-role-{i}
      AssumeRolePolicyDocument:
        Version: '2012-10-17'
        Statement:
          - Effect: Allow
            Principal:
              Service: {service}
            Action: sts:AssumeRole
      ManagedPolicyArns:
        - arn:aws:iam::aws:policy/ReadOnlyAccess
      Tags:
{cfn_tags(f'{env_id}-role-{i}')}
"""

    # IAM Policies
    for i in range(counts.iam_policies):
        template += f"""
  Policy{i}:
    Type: AWS::IAM::ManagedPolicy
    Properties:
      ManagedPolicyName: {env_id}-policy-{i}
      PolicyDocument:
        Version: '2012-10-17'
        Statement:
          - Effect: Allow
            Action:
              - logs:CreateLogGroup
              - logs:CreateLogStream
              - logs:PutLogEvents
            Resource: '*'
"""

    # Instance profiles
    for i in range(counts.instance_profiles):
        template += f"""
  InstanceProfile{i}:
    Type: AWS::IAM::InstanceProfile
    Properties:
      InstanceProfileName: {env_id}-instance-profile-{i}
      Roles: []
"""

    if outputs:
        template += "\nOutputs:\n" + "\n".join(outputs)

    return template


def generate_cfn_storage(counts: ResourceCounts, env_id: str, networking_stack: str, sg_stack: str) -> str:
    """Generate CloudFormation template for storage resources."""
    template = CFN_HEADER.format(description=f"Storage stack for perf test {env_id}")
    template += "Resources:\n"

    # S3 Buckets
    for i in range(counts.s3_buckets):
        template += f"""
  Bucket{i}:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: {env_id}-bucket-{i}
      VersioningConfiguration:
        Status: Enabled
      PublicAccessBlockConfiguration:
        BlockPublicAcls: true
        BlockPublicPolicy: true
        IgnorePublicAcls: true
        RestrictPublicBuckets: true
      BucketEncryption:
        ServerSideEncryptionConfiguration:
          - ServerSideEncryptionByDefault:
              SSEAlgorithm: AES256
      Tags:
{cfn_tags(f'{env_id}-bucket-{i}')}
"""

    # DynamoDB Tables
    for i in range(counts.dynamodb_tables):
        template += f"""
  DynamoTable{i}:
    Type: AWS::DynamoDB::Table
    Properties:
      TableName: {env_id}-table-{i}
      BillingMode: PAY_PER_REQUEST
      AttributeDefinitions:
        - AttributeName: pk
          AttributeType: S
        - AttributeName: sk
          AttributeType: S
      KeySchema:
        - AttributeName: pk
          KeyType: HASH
        - AttributeName: sk
          KeyType: RANGE
      DeletionProtectionEnabled: false
      Tags:
{cfn_tags(f'{env_id}-table-{i}')}
"""

    # EFS File Systems
    for i in range(counts.efs_filesystems):
        template += f"""
  EFS{i}:
    Type: AWS::EFS::FileSystem
    Properties:
      PerformanceMode: generalPurpose
      Encrypted: true
      FileSystemTags:
{cfn_tags(f'{env_id}-efs-{i}')}
"""

    # EFS Mount Targets
    for i in range(min(counts.efs_mount_targets, counts.efs_filesystems)):
        vpc_idx = i % counts.vpcs
        private_subnet_idx = counts.subnets_per_vpc // 2  # First private subnet
        template += f"""
  EFSMountTarget{i}:
    Type: AWS::EFS::MountTarget
    Properties:
      FileSystemId: !Ref EFS{i}
      SubnetId: !ImportValue {networking_stack}-Subnet{vpc_idx}x{private_subnet_idx}Id
      SecurityGroups:
        - !ImportValue {sg_stack}-SG0Id
"""

    return template


def generate_cfn_compute(counts: ResourceCounts, env_id: str, region: str, networking_stack: str, sg_stack: str) -> str:
    """Generate CloudFormation template for compute resources."""
    # AMI map (same as PKL)
    ami_map = {
        "us-east-1": "ami-0c02fb55b2c6c5bdc",
        "us-east-2": "ami-06ba60a55dcbf8bf2",
        "us-west-1": "ami-0ecb7bb3a2c9a8b9d",
        "us-west-2": "ami-0c2ab3b8efb09f272",
        "eu-west-1": "ami-0c1c30571d2dae5c9",
        "eu-central-1": "ami-0a49b025fffbbdac6",
        "ap-southeast-1": "ami-0c802847a7dd848c0",
    }
    ami_id = ami_map.get(region, ami_map["us-east-1"])

    template = CFN_HEADER.format(description=f"Compute stack for perf test {env_id}")
    template += "Resources:\n"

    # Launch templates
    for i in range(counts.launch_templates):
        template += f"""
  LaunchTemplate{i}:
    Type: AWS::EC2::LaunchTemplate
    Properties:
      LaunchTemplateName: {env_id}-lt-{i}
      LaunchTemplateData:
        ImageId: {ami_id}
        InstanceType: t3.micro
        Monitoring:
          Enabled: true
        BlockDeviceMappings:
          - DeviceName: /dev/xvda
            Ebs:
              VolumeSize: 8
              VolumeType: gp3
              DeleteOnTermination: true
"""

    # EC2 Instances
    instance_types = ["t3.micro", "t3.small", "t3.medium"]
    for i in range(counts.ec2_instances):
        vpc_idx = i % counts.vpcs
        sg_idx = i % counts.security_groups
        instance_type = instance_types[i % len(instance_types)]
        private_subnet_idx = counts.subnets_per_vpc // 2

        template += f"""
  Instance{i}:
    Type: AWS::EC2::Instance
    Properties:
      ImageId: {ami_id}
      InstanceType: {instance_type}
      SubnetId: !ImportValue {networking_stack}-Subnet{vpc_idx}x{private_subnet_idx}Id
      SecurityGroupIds:
        - !ImportValue {sg_stack}-SG{sg_idx}Id
      Monitoring: true
      Tags:
{cfn_tags(f'{env_id}-instance-{i}')}
"""

    # EBS Volumes
    azs = ["a", "b", "c"]
    private_subnet_idx = counts.subnets_per_vpc // 2
    private_subnet_az = azs[private_subnet_idx % 3]

    standalone_volumes = counts.ebs_volumes // 2
    for i in range(counts.ebs_volumes):
        if i < standalone_volumes:
            az_suffix = azs[i % 3]
        else:
            az_suffix = private_subnet_az

        vol_type = ["gp3", "gp2"][i % 2]
        encrypted = str(i % 2 == 0).lower()

        template += f"""
  Volume{i}:
    Type: AWS::EC2::Volume
    Properties:
      AvailabilityZone: {region}{az_suffix}
      Size: {20 + (i % 5) * 10}
      VolumeType: {vol_type}
      Encrypted: {encrypted}
      Tags:
{cfn_tags(f'{env_id}-volume-{i}')}
"""

    return template


def generate_cfn_observability(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for observability resources."""
    template = CFN_HEADER.format(description=f"Observability stack for perf test {env_id}")
    template += "Resources:\n"

    # CloudWatch Log Groups
    for i in range(counts.log_groups):
        template += f"""
  LogGroup{i}:
    Type: AWS::Logs::LogGroup
    Properties:
      LogGroupName: /perf-test/{env_id}/app-{i}
      RetentionInDays: 7
      Tags:
{cfn_tags(f'{env_id}-log-group-{i}')}
"""

    # SQS Queues
    for i in range(counts.sqs_queues):
        template += f"""
  SQSQueue{i}:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: {env_id}-queue-{i}
      VisibilityTimeout: 30
      MessageRetentionPeriod: 345600
      Tags:
{cfn_tags(f'{env_id}-sqs-queue-{i}')}
"""

    return template


def generate_cfn_secrets(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for secrets."""
    template = CFN_HEADER.format(description=f"Secrets stack for perf test {env_id}")
    template += "Resources:\n"

    for i in range(counts.secrets):
        template += f"""
  Secret{i}:
    Type: AWS::SecretsManager::Secret
    Properties:
      Name: {env_id}/secret-{i}
      Description: Secret {i} for perf test
      GenerateSecretString:
        SecretStringTemplate: '{{"username": "admin-{i}"}}'
        GenerateStringKey: password
        PasswordLength: 32
        ExcludePunctuation: true
      Tags:
{cfn_tags(f'{env_id}-secret-{i}')}
"""

    return template


def generate_cfn_kms(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for KMS resources."""
    template = CFN_HEADER.format(description=f"KMS stack for perf test {env_id}")
    template += "Resources:\n"

    outputs = []

    for i in range(counts.kms_keys):
        template += f"""
  KMSKey{i}:
    Type: AWS::KMS::Key
    Properties:
      Description: KMS key {i} for perf test
      EnableKeyRotation: true
      KeyPolicy:
        Version: '2012-10-17'
        Statement:
          - Sid: Enable IAM User Permissions
            Effect: Allow
            Principal:
              AWS: !Sub 'arn:aws:iam::${{AWS::AccountId}}:root'
            Action: kms:*
            Resource: '*'
      Tags:
{cfn_tags(f'{env_id}-kms-key-{i}')}
"""
        outputs.append(f"  KMSKey{i}Arn:\n    Value: !GetAtt KMSKey{i}.Arn\n    Export:\n      Name: !Sub '${{AWS::StackName}}-KMSKey{i}Arn'")

    for i in range(min(counts.kms_aliases, counts.kms_keys)):
        template += f"""
  KMSAlias{i}:
    Type: AWS::KMS::Alias
    Properties:
      AliasName: alias/{env_id}-key-{i}
      TargetKeyId: !Ref KMSKey{i}
"""

    if outputs:
        template += "\nOutputs:\n" + "\n".join(outputs)

    return template


def generate_cfn_application(counts: ResourceCounts, env_id: str, iam_stack: str) -> str:
    """Generate CloudFormation template for application resources."""
    template = CFN_HEADER.format(description=f"Application stack for perf test {env_id}")
    template += "Resources:\n"

    # Calculate lambda_roles_count same as PKL
    min_other_roles = 1 if (counts.iam_policies > 0 or counts.instance_profiles > 0) else 0
    lambda_roles_count = min(
        max(5, int(counts.iam_roles * 0.2)),
        counts.iam_roles - min_other_roles
    )

    # Lambda functions
    for i in range(counts.lambdas):
        role_idx = i % lambda_roles_count
        template += f"""
  Lambda{i}:
    Type: AWS::Lambda::Function
    Properties:
      FunctionName: {env_id}-lambda-{i}
      Runtime: python3.11
      Handler: index.handler
      Role: !ImportValue {iam_stack}-LambdaRole{role_idx}Arn
      Code:
        ZipFile: |
          def handler(event, context):
              return {{'statusCode': 200}}
      MemorySize: 128
      Timeout: 30
      Tags:
{cfn_tags(f'{env_id}-lambda-{i}')}
"""

    # ECR Repositories
    for i in range(counts.ecr_repos):
        template += f"""
  ECRRepo{i}:
    Type: AWS::ECR::Repository
    Properties:
      RepositoryName: {env_id}-repo-{i}
      ImageScanningConfiguration:
        ScanOnPush: true
      ImageTagMutability: MUTABLE
      Tags:
{cfn_tags(f'{env_id}-ecr-repo-{i}')}
"""

    return template


def generate_cfn_route53(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for Route53 resources."""
    template = CFN_HEADER.format(description=f"Route53 stack for perf test {env_id}")
    template += "Resources:\n"

    outputs = []

    for i in range(counts.route53_zones):
        template += f"""
  HostedZone{i}:
    Type: AWS::Route53::HostedZone
    Properties:
      Name: {env_id}-zone-{i}.perftest.internal
      HostedZoneConfig:
        Comment: Hosted zone {i} for perf test
      HostedZoneTags:
{cfn_tags(f'{env_id}-zone-{i}')}
"""
        outputs.append(f"  HostedZone{i}Id:\n    Value: !Ref HostedZone{i}\n    Export:\n      Name: !Sub '${{AWS::StackName}}-HostedZone{i}Id'")

    record_types = ["A", "CNAME", "TXT"]
    for i in range(counts.route53_records):
        zone_idx = i % counts.route53_zones
        record_type = record_types[i % len(record_types)]

        if record_type == "A":
            resource_records = "        - 1.2.3.4\n        - 5.6.7.8"
        elif record_type == "CNAME":
            resource_records = "        - target.perftest.internal"
        else:  # TXT
            resource_records = '        - \'"v=spf1 -all"\''

        template += f"""
  Record{i}:
    Type: AWS::Route53::RecordSet
    Properties:
      HostedZoneId: !Ref HostedZone{zone_idx}
      Name: record-{i}.{env_id}-zone-{zone_idx}.perftest.internal
      Type: {record_type}
      TTL: 300
      ResourceRecords:
{resource_records}
"""

    if outputs:
        template += "\nOutputs:\n" + "\n".join(outputs)

    return template


def generate_cfn_api_gateway(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for API Gateway resources."""
    template = CFN_HEADER.format(description=f"API Gateway stack for perf test {env_id}")
    template += "Resources:\n"

    for i in range(counts.api_gateways):
        template += f"""
  RestApi{i}:
    Type: AWS::ApiGateway::RestApi
    Properties:
      Name: {env_id}-api-{i}
      Description: API Gateway {i} for perf test
      EndpointConfiguration:
        Types:
          - REGIONAL
      Tags:
{cfn_tags(f'{env_id}-api-{i}')}
"""

    return template


def generate_cfn_rds(counts: ResourceCounts, env_id: str, networking_stack: str, sg_stack: str) -> str:
    """Generate CloudFormation template for RDS resources."""
    template = CFN_HEADER.format(description=f"RDS stack for perf test {env_id}")
    template += "Resources:\n"

    # DB Parameter Groups
    engines = ["mysql8.0", "postgres14", "mariadb10.6"]
    for i in range(counts.db_parameter_groups):
        engine_family = engines[i % len(engines)]
        template += f"""
  DBParamGroup{i}:
    Type: AWS::RDS::DBParameterGroup
    Properties:
      DBParameterGroupName: {env_id}-db-param-group-{i}
      Description: DB parameter group {i} for perf test
      Family: {engine_family}
      Tags:
{cfn_tags(f'{env_id}-db-param-group-{i}')}
"""

    return template


def generate_cfn_deploy_script(env_id: str, region: str, stacks: list) -> str:
    """Generate deployment shell script."""
    deploy_commands = []
    for stack in stacks:
        deploy_commands.append(f"""
echo "Deploying {stack['name']}..."
aws cloudformation deploy \\
    --stack-name {env_id}-{stack['name']} \\
    --template-file {stack['file']} \\
    --capabilities CAPABILITY_NAMED_IAM \\
    --region {region} \\
    --no-fail-on-empty-changeset
""")

    return f"""#!/bin/bash
# Deploy script for {env_id}
# Generated by generate-perf-test-env.py

set -e

REGION="{region}"
ENV_ID="{env_id}"

echo "Deploying CloudFormation stacks for $ENV_ID in $REGION"
echo "=========================================="
{"".join(deploy_commands)}
echo "=========================================="
echo "Deployment complete!"
echo ""
echo "To destroy all stacks, run: ./destroy.sh"
"""


def generate_cfn_destroy_script(env_id: str, region: str, stacks: list) -> str:
    """Generate destruction shell script."""
    # Reverse order for deletion
    reversed_stacks = list(reversed(stacks))
    destroy_commands = []
    for stack in reversed_stacks:
        destroy_commands.append(f"""
echo "Deleting {stack['name']}..."
aws cloudformation delete-stack \\
    --stack-name {env_id}-{stack['name']} \\
    --region {region}
aws cloudformation wait stack-delete-complete \\
    --stack-name {env_id}-{stack['name']} \\
    --region {region} || true
""")

    return f"""#!/bin/bash
# Destroy script for {env_id}
# Generated by generate-perf-test-env.py

set -e

REGION="{region}"
ENV_ID="{env_id}"

echo "Destroying CloudFormation stacks for $ENV_ID in $REGION"
echo "=========================================="
{"".join(destroy_commands)}
echo "=========================================="
echo "Destruction complete!"
"""


def generate_cfn_readme(env_id: str, region: str, counts: ResourceCounts) -> str:
    """Generate README for CloudFormation deployment."""
    return f"""# Performance Test Environment: {env_id} (CloudFormation)

## Overview

This directory contains CloudFormation templates that create a large-scale AWS infrastructure
environment for testing formae's discovery feature. The environment creates approximately
**{counts.total} resources** distributed across various AWS services.

## Deployment

### Prerequisites

1. AWS CLI configured with appropriate credentials
2. Sufficient IAM permissions to create resources

### Deploy All Stacks

```bash
chmod +x deploy.sh destroy.sh
./deploy.sh
```

### Destroy All Stacks

```bash
./destroy.sh
```

## Testing Discovery

After deployment, you can test formae's discovery feature:

```bash
# Start the formae agent
formae agent start

# Run discovery for AWS
formae discover --target aws --region {region}

# View discovered resources
formae inventory --unmanaged
```

## Stack Order

The stacks are deployed in dependency order:
1. `networking` - VPCs, Subnets, Route Tables
2. `security-groups` - Security Groups (depends on networking)
3. `iam` - IAM Roles and Policies
4. `kms` - KMS Keys
5. `storage` - S3, DynamoDB, EFS (depends on networking, security-groups)
6. `compute` - EC2, EBS (depends on networking, security-groups)
7. `observability` - CloudWatch, SQS
8. `secrets` - Secrets Manager
9. `application` - Lambda, ECR (depends on iam)
10. `route53` - DNS
11. `api-gateway` - API Gateway
12. `rds` - RDS Parameter Groups

## Resource Distribution

| Category | Count |
|----------|-------|
| Networking | {counts.vpcs + counts.vpcs * counts.subnets_per_vpc + counts.route_tables + counts.igws + counts.nat_gws} |
| Security Groups | {counts.security_groups + counts.sg_ingress_rules + counts.sg_egress_rules} |
| Compute | {counts.ec2_instances + counts.ebs_volumes + counts.launch_templates} |
| IAM | {counts.iam_roles + counts.iam_policies + counts.instance_profiles} |
| Storage | {counts.s3_buckets + counts.dynamodb_tables + counts.efs_filesystems + counts.efs_mount_targets} |
| Observability | {counts.log_groups + counts.sqs_queues} |
| Application | {counts.lambdas + counts.ecr_repos} |
| DNS & API | {counts.route53_zones + counts.route53_records + counts.api_gateways} |
| RDS | {counts.db_parameter_groups} |
| Secrets | {counts.secrets} |
| KMS | {counts.kms_keys + counts.kms_aliases} |

## Cost Warning

This environment creates real AWS resources that will incur costs.
**Always destroy the environment when testing is complete!**

## Configuration

- **Environment ID**: `{env_id}`
- **Region**: `{region}`
"""


def main():
    parser = argparse.ArgumentParser(
        description="Generate a large-scale AWS infrastructure environment for performance testing formae.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Generate PKL files for formae
  %(prog)s --count 100 --region us-east-1
  %(prog)s --count 1000 --region eu-west-1 --output ./perf-test

  # Generate CloudFormation templates for discovery testing
  %(prog)s --format cloudformation --count 100 --region us-east-1
  %(prog)s -f cfn -c 500 -r eu-west-1 -o ./discovery-test
        """
    )

    parser.add_argument(
        "--format", "-f",
        type=str,
        choices=["pkl", "cloudformation", "cfn"],
        default="pkl",
        help="Output format: 'pkl' for formae, 'cloudformation'/'cfn' for AWS CloudFormation (default: pkl)"
    )

    parser.add_argument(
        "--count", "-c",
        type=int,
        default=100,
        help="Target number of resources to generate (default: 100)"
    )

    parser.add_argument(
        "--region", "-r",
        type=str,
        default="us-east-1",
        help="AWS region for the environment (default: us-east-1)"
    )

    parser.add_argument(
        "--output", "-o",
        type=str,
        default=None,
        help="Output directory (default: ./perf-test-<uuid>)"
    )

    parser.add_argument(
        "--formae-path",
        type=str,
        default=None,
        help="Path to formae repository (default: auto-detect from script location)"
    )

    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Print resource counts without generating files"
    )

    args = parser.parse_args()

    # Generate unique environment ID
    env_id = f"perf-{uuid.uuid4().hex[:8]}"
    stack_name = f"perf-test-{env_id}"

    # Calculate resource counts
    counts = calculate_counts(args.count)

    if args.dry_run:
        print(f"Target count: {args.count}")
        print(f"Calculated total: {counts.total}")
        print(f"\nResource breakdown:")
        print(f"\n  Networking (capped to stay within quotas):")
        print(f"    VPCs: {counts.vpcs} (hard cap at 5)")
        print(f"    Subnets: {counts.vpcs * counts.subnets_per_vpc} ({counts.subnets_per_vpc} per VPC)")
        print(f"    Route Tables: {counts.route_tables}")
        print(f"    Routes: {counts.routes}")
        print(f"    Internet Gateways: {counts.igws} (1 per VPC)")
        print(f"    NAT Gateways: {counts.nat_gws} (capped at 5)")
        print(f"    Elastic IPs: {counts.eips}")
        print(f"    Security Groups: {counts.security_groups}")
        print(f"    SG Ingress Rules: {counts.sg_ingress_rules}")
        print(f"    SG Egress Rules: {counts.sg_egress_rules}")
        print(f"    VPC Endpoints: {counts.vpc_endpoints}")
        print(f"\n  Compute:")
        print(f"    EC2 Instances: {counts.ec2_instances}")
        print(f"    EBS Volumes: {counts.ebs_volumes}")
        print(f"    Launch Templates: {counts.launch_templates}")
        print(f"\n  IAM & Security:")
        print(f"    IAM Roles: {counts.iam_roles}")
        print(f"    IAM Policies: {counts.iam_policies}")
        print(f"    Instance Profiles: {counts.instance_profiles}")
        print(f"    KMS Keys: {counts.kms_keys}")
        print(f"    KMS Aliases: {counts.kms_aliases}")
        print(f"    Secrets: {counts.secrets}")
        print(f"\n  Storage:")
        print(f"    S3 Buckets: {counts.s3_buckets}")
        print(f"    DynamoDB Tables: {counts.dynamodb_tables}")
        print(f"    EFS File Systems: {counts.efs_filesystems}")
        print(f"    EFS Mount Targets: {counts.efs_mount_targets}")
        print(f"\n  Observability:")
        print(f"    CloudWatch Log Groups: {counts.log_groups}")
        print(f"    SQS Queues: {counts.sqs_queues}")
        print(f"\n  Application:")
        print(f"    Lambda Functions: {counts.lambdas}")
        print(f"    ECR Repositories: {counts.ecr_repos}")
        print(f"\n  DNS & API Gateway:")
        print(f"    Route53 Hosted Zones: {counts.route53_zones}")
        print(f"    Route53 Records: {counts.route53_records}")
        print(f"    API Gateways: {counts.api_gateways}")
        print(f"    API Stages: {counts.api_stages}")
        print(f"\n  RDS:")
        print(f"    RDS Instances: {counts.rds_instances}")
        print(f"    DB Subnet Groups: {counts.db_subnet_groups}")
        print(f"    DB Parameter Groups: {counts.db_parameter_groups}")
        print(f"\n  Implicit Resources:")
        print(f"    VPC Gateway Attachments: {counts.vpcs}")
        print(f"    Subnet Route Table Associations: {counts.vpcs * counts.subnets_per_vpc}")
        print(f"    EBS Volume Attachments: ~{counts.ec2_instances * 2}")
        return

    # Determine output directory
    if args.output:
        output_dir = args.output
    else:
        output_dir = f"./perf-test-{env_id}"

    # Determine formae path
    if args.formae_path:
        formae_path = args.formae_path
    else:
        # Auto-detect from script location
        script_dir = os.path.dirname(os.path.abspath(__file__))
        formae_path = os.path.dirname(script_dir)

    # Create output directory
    os.makedirs(output_dir, exist_ok=True)

    # Normalize format
    output_format = args.format if args.format != "cfn" else "cloudformation"

    if output_format == "pkl":
        # Generate PKL files for formae
        files = {
            "PklProject": generate_pkl_project(formae_path),
            "vars.pkl": generate_vars_pkl(env_id, args.region, stack_name),
            "networking.pkl": generate_networking_pkl(counts, args.region),
            "security_groups.pkl": generate_security_groups_pkl(counts),
            "compute.pkl": generate_compute_pkl(counts, args.region),
            "iam.pkl": generate_iam_pkl(counts),
            "kms.pkl": generate_kms_pkl(counts),
            "storage.pkl": generate_storage_pkl(counts),
            "observability.pkl": generate_observability_pkl(counts),
            "secrets.pkl": generate_secrets_pkl(counts),
            "application.pkl": generate_application_pkl(counts),
            "route53.pkl": generate_route53_pkl(counts),
            "api_gateway.pkl": generate_api_gateway_pkl(counts),
            "rds.pkl": generate_rds_pkl(counts),
            "main.pkl": generate_main_pkl(env_id, stack_name, counts),
            "README.md": generate_readme(env_id, stack_name, args.region, counts),
        }

        for filename, content in files.items():
            filepath = os.path.join(output_dir, filename)
            with open(filepath, "w") as f:
                f.write(content)
            print(f"Generated: {filepath}")

        print(f"\n{'=' * 60}")
        print(f"Performance test environment generated successfully!")
        print(f"{'=' * 60}")
        print(f"\nFormat: PKL (for formae)")
        print(f"Environment ID: {env_id}")
        print(f"Stack Name: {stack_name}")
        print(f"Region: {args.region}")
        print(f"Target Resources: {args.count}")
        print(f"Calculated Resources: {counts.total}")
        print(f"Output Directory: {output_dir}")
        print(f"\nNext steps:")
        print(f"  1. cd {output_dir}")
        print(f"  2. pkl project resolve")
        print(f"  3. formae apply --simulate main.pkl  # dry-run")
        print(f"  4. formae apply main.pkl             # deploy")
        print(f"  5. formae destroy --stack {stack_name}  # cleanup")

    else:
        # Generate CloudFormation templates
        networking_stack = f"{env_id}-networking"
        sg_stack = f"{env_id}-security-groups"
        iam_stack = f"{env_id}-iam"

        # Define stacks in deployment order
        stacks = [
            {"name": "networking", "file": "networking.yaml"},
            {"name": "security-groups", "file": "security-groups.yaml"},
            {"name": "iam", "file": "iam.yaml"},
            {"name": "kms", "file": "kms.yaml"},
            {"name": "storage", "file": "storage.yaml"},
            {"name": "compute", "file": "compute.yaml"},
            {"name": "observability", "file": "observability.yaml"},
            {"name": "secrets", "file": "secrets.yaml"},
            {"name": "application", "file": "application.yaml"},
            {"name": "route53", "file": "route53.yaml"},
            {"name": "api-gateway", "file": "api-gateway.yaml"},
            {"name": "rds", "file": "rds.yaml"},
        ]

        files = {
            "networking.yaml": generate_cfn_networking(counts, env_id, args.region),
            "security-groups.yaml": generate_cfn_security_groups(counts, env_id, networking_stack),
            "iam.yaml": generate_cfn_iam(counts, env_id),
            "kms.yaml": generate_cfn_kms(counts, env_id),
            "storage.yaml": generate_cfn_storage(counts, env_id, networking_stack, sg_stack),
            "compute.yaml": generate_cfn_compute(counts, env_id, args.region, networking_stack, sg_stack),
            "observability.yaml": generate_cfn_observability(counts, env_id),
            "secrets.yaml": generate_cfn_secrets(counts, env_id),
            "application.yaml": generate_cfn_application(counts, env_id, iam_stack),
            "route53.yaml": generate_cfn_route53(counts, env_id),
            "api-gateway.yaml": generate_cfn_api_gateway(counts, env_id),
            "rds.yaml": generate_cfn_rds(counts, env_id, networking_stack, sg_stack),
            "deploy.sh": generate_cfn_deploy_script(env_id, args.region, stacks),
            "destroy.sh": generate_cfn_destroy_script(env_id, args.region, stacks),
            "README.md": generate_cfn_readme(env_id, args.region, counts),
        }

        for filename, content in files.items():
            filepath = os.path.join(output_dir, filename)
            with open(filepath, "w") as f:
                f.write(content)
            print(f"Generated: {filepath}")

        # Make scripts executable
        os.chmod(os.path.join(output_dir, "deploy.sh"), 0o755)
        os.chmod(os.path.join(output_dir, "destroy.sh"), 0o755)

        print(f"\n{'=' * 60}")
        print(f"Performance test environment generated successfully!")
        print(f"{'=' * 60}")
        print(f"\nFormat: CloudFormation (for discovery testing)")
        print(f"Environment ID: {env_id}")
        print(f"Region: {args.region}")
        print(f"Target Resources: {args.count}")
        print(f"Calculated Resources: {counts.total}")
        print(f"Output Directory: {output_dir}")
        print(f"\nNext steps:")
        print(f"  1. cd {output_dir}")
        print(f"  2. ./deploy.sh             # deploy all stacks")
        print(f"  3. # Test formae discovery:")
        print(f"     formae agent start")
        print(f"     formae discover --target aws --region {args.region}")
        print(f"     formae inventory --unmanaged")
        print(f"  4. ./destroy.sh            # cleanup")


if __name__ == "__main__":
    main()
