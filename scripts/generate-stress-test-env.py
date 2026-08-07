#!/usr/bin/env python3
# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

"""
Generate a large-scale multi-cloud infrastructure environment for performance testing formae.

This script generates either PKL files (for formae) or CloudFormation templates
(for creating resources outside formae, useful for discovery testing).

The distribution of resource types mimics typical enterprise production environments.
Supports AWS, Azure, and GCP resources either individually or combined.

Usage:
    # Generate PKL files for formae (AWS only - default)
    python3 scripts/generate-perf-test-env.py --count 1000 --region us-east-1
    python3 scripts/generate-perf-test-env.py --count 3000 --region eu-west-1 --output ./perf-test

    # Generate multi-cloud PKL files
    python3 scripts/generate-perf-test-env.py --count 100 --clouds aws,azure,gcp \\
        --region us-east-2 --subscription-azure <sub-id> --project-gcp <project-id>

    # Use scale profiles for multi-cloud
    python3 scripts/generate-perf-test-env.py --profile medium --clouds aws,azure,gcp \\
        --region us-east-2 --subscription-azure <sub-id> --project-gcp <project-id>

    # Generate CloudFormation templates (for discovery testing, AWS only)
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


# CloudFormation limits
MAX_RESOURCES_PER_STACK = 400  # CloudFormation limit is 500, leave margin for safety

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

# Quota-safe distribution - avoids resources with tight default quotas
# Skips: EC2, EBS, EIP, NAT GW, VPC, Subnets, Security Groups, EFS (requires VPC)
# Focuses on: S3, IAM, KMS, Secrets, DynamoDB, CloudWatch, SQS, Lambda, ECR, Route53, API GW
DISTRIBUTION_QUOTA_SAFE = {
    # IAM & Security (25%)
    "iam_role": 0.10,           # IAM roles (limit 1000)
    "iam_policy": 0.08,         # IAM policies (limit 1500)
    "instance_profile": 0.04,   # Instance profiles
    "kms_key": 0.02,            # KMS keys (limit 10000)
    "kms_alias": 0.02,          # KMS aliases
    "secret": 0.04,             # Secrets (limit 500K)

    # Storage (25%)
    "s3_bucket": 0.15,          # S3 buckets (soft limit, easily increased)
    "dynamodb_table": 0.10,     # DynamoDB tables (limit 2500)

    # Observability (20%)
    "log_group": 0.12,          # CloudWatch log groups (limit 1M)
    "sqs_queue": 0.08,          # SQS queues (no practical limit)

    # Application (20%)
    "lambda": 0.12,             # Lambda functions (code storage limit, not count)
    "ecr_repo": 0.08,           # ECR repositories (limit 10K)

    # DNS & API Gateway (10%)
    "route53_zone": 0.03,       # Route53 hosted zones (limit 500)
    "route53_record": 0.04,     # Route53 records
    "api_gateway": 0.02,        # API Gateway REST APIs (limit 600)
    "api_stage": 0.01,          # API Gateway stages
}


# Azure resource distribution for realistic production environments
# Based on typical enterprise Azure usage patterns
AZURE_DISTRIBUTION = {
    # Networking
    "resource_group": 0.06,         # Azure::Resources::ResourceGroup
    "virtual_network": 0.04,        # Azure::Resources::VirtualNetwork
    "subnet": 0.04,                 # Azure::Resources::Subnet
    "nsg": 0.05,                    # Azure::Network::NetworkSecurityGroup
    "public_ip": 0.03,              # Azure::Network::PublicIPAddress

    # Compute
    "virtual_machine": 0.06,        # Azure::Compute::VirtualMachine
    "network_interface": 0.06,      # Azure::Network::NetworkInterface

    # Storage
    "storage_account": 0.10,        # Azure::Storage::StorageAccount

    # IAM
    "managed_identity": 0.08,       # Azure::ManagedIdentity::UserAssignedIdentity

    # Security
    "key_vault": 0.06,              # Azure::KeyVault::Vault

    # Container
    "container_registry": 0.03,     # Azure::ContainerRegistry::Registry

    # Database
    "postgres_server": 0.02,        # Azure::DBforPostgreSQL::FlexibleServer
}


# GCP resource distribution for realistic production environments
# NOTE: IAM ServiceAccount and Artifact Registry are not in the current GCP plugin schema,
# so we redistribute those percentages to other available resource types.
GCP_DISTRIBUTION = {
    # Compute
    "compute_instance": 0.06,       # GCP::Compute::Instance
    "compute_disk": 0.04,           # GCP::Compute::Disk

    # Networking
    "compute_network": 0.04,        # GCP::Compute::Network
    "compute_subnetwork": 0.04,     # GCP::Compute::Subnetwork
    "compute_firewall": 0.05,       # GCP::Compute::Firewall
    "compute_address": 0.03,        # GCP::Compute::Address

    # Storage
    "storage_bucket": 0.12,         # GCP::Storage::Bucket (bumped, absorbing IAM/AR share)

    # Database
    "sql_instance": 0.02,           # GCP::SQL::DatabaseInstance

    # BigQuery
    "bigquery_dataset": 0.05,       # GCP::BigQuery::Dataset (bumped, absorbing IAM/AR share)
}


# Scale profiles: maps profile name -> (aws_count, azure_count, gcp_count)
SCALE_PROFILES = {
    "small":  (200, 150, 150),
    "medium": (2000, 1500, 1500),
    "large":  (8000, 6000, 6000),
    "xl":     (20000, 15000, 15000),
}


@dataclass
class AzureResourceCounts:
    """Calculated resource counts for Azure based on total target."""
    resource_groups: int
    virtual_networks: int
    subnets_per_vnet: int
    nsgs: int
    public_ips: int
    virtual_machines: int
    network_interfaces: int
    storage_accounts: int
    managed_identities: int
    key_vaults: int
    container_registries: int
    postgres_servers: int

    @property
    def total(self) -> int:
        return (
            self.resource_groups +
            self.virtual_networks +
            (self.virtual_networks * self.subnets_per_vnet) +
            self.nsgs +
            self.public_ips +
            self.virtual_machines +
            self.network_interfaces +
            self.storage_accounts +
            self.managed_identities +
            self.key_vaults +
            self.container_registries +
            self.postgres_servers
        )


def calculate_azure_counts(total: int) -> AzureResourceCounts:
    """Calculate Azure resource counts based on target total and distribution.

    Quota caps:
    - Resource Groups: cap at 50 (limit 980)
    - VNets: cap at 50 (limit 1000)
    - Storage Accounts: cap at 200 (limit 250)
    - Key Vaults: cap at 200
    - VMs: cap conservatively
    """
    d = AZURE_DISTRIBUTION

    rgs = min(50, max(1, int(total * d["resource_group"])))
    vnets = min(50, max(1, int(total * d["virtual_network"])))
    subnets_per_vnet = max(1, min(6, int(total * d["subnet"] / max(1, vnets))))

    return AzureResourceCounts(
        resource_groups=rgs,
        virtual_networks=vnets,
        subnets_per_vnet=subnets_per_vnet,
        nsgs=max(1, int(total * d["nsg"])),
        public_ips=max(1, int(total * d["public_ip"])),
        virtual_machines=max(1, int(total * d["virtual_machine"])),
        network_interfaces=max(1, int(total * d["network_interface"])),
        storage_accounts=min(200, max(1, int(total * d["storage_account"]))),
        managed_identities=max(1, int(total * d["managed_identity"])),
        key_vaults=min(200, max(1, int(total * d["key_vault"]))),
        container_registries=max(1, int(total * d["container_registry"])),
        postgres_servers=max(0, int(total * d["postgres_server"])),
    )


@dataclass
class GCPResourceCounts:
    """Calculated resource counts for GCP based on total target."""
    compute_instances: int
    compute_disks: int
    compute_networks: int
    compute_subnetworks: int
    compute_firewalls: int
    compute_addresses: int
    storage_buckets: int
    sql_instances: int
    bigquery_datasets: int

    @property
    def total(self) -> int:
        return (
            self.compute_instances +
            self.compute_disks +
            self.compute_networks +
            self.compute_subnetworks +
            self.compute_firewalls +
            self.compute_addresses +
            self.storage_buckets +
            self.sql_instances +
            self.bigquery_datasets
        )


def calculate_gcp_counts(total: int) -> GCPResourceCounts:
    """Calculate GCP resource counts based on target total and distribution.

    Quota caps:
    - Networks: cap at 15 (default limit)
    - Firewall rules: cap at 400 (limit 500)
    - Cloud SQL instances: cap at 30 (limit 40)
    """
    d = GCP_DISTRIBUTION

    return GCPResourceCounts(
        compute_instances=max(1, int(total * d["compute_instance"])),
        compute_disks=max(1, int(total * d["compute_disk"])),
        compute_networks=min(15, max(1, int(total * d["compute_network"]))),
        compute_subnetworks=max(1, int(total * d["compute_subnetwork"])),
        compute_firewalls=min(400, max(1, int(total * d["compute_firewall"]))),
        compute_addresses=max(1, int(total * d["compute_address"])),
        storage_buckets=max(1, int(total * d["storage_bucket"])),
        sql_instances=min(30, max(0, int(total * d["sql_instance"]))),
        bigquery_datasets=max(1, int(total * d["bigquery_dataset"])),
    )


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


def calculate_counts_quota_safe(total: int) -> ResourceCounts:
    """Calculate resource counts for quota-safe profile.

    This profile avoids resources with tight default AWS quotas:
    - No EC2 instances (default 5 vCPU quota)
    - No Elastic IPs (default 5 quota)
    - No NAT Gateways (require EIPs)
    - No VPCs/Subnets (avoid networking complexity)
    - No EFS (requires VPC/subnets)

    Focus on high-limit resources: S3, IAM, KMS, DynamoDB, CloudWatch, SQS, Lambda, ECR, Route53, API GW

    Caps applied to stay within default quotas:
    - IAM Roles: 950 (default limit 1000)
    - IAM Policies: 1400 (default limit 1500)
    - DynamoDB Tables: 2400 (default limit 2500)
    - Route53 Hosted Zones: 450 (default limit 500)
    - API Gateways: 550 (default limit 600)
    """
    d = DISTRIBUTION_QUOTA_SAFE
    return ResourceCounts(
        # Networking - all zeroed out
        vpcs=0,
        subnets_per_vpc=0,
        route_tables=0,
        routes=0,
        igws=0,
        nat_gws=0,
        eips=0,
        security_groups=0,
        sg_ingress_rules=0,
        sg_egress_rules=0,
        vpc_endpoints=0,
        # Compute - all zeroed out
        ec2_instances=0,
        ebs_volumes=0,
        launch_templates=0,
        # IAM & Security - use quota-safe distribution with caps
        iam_roles=min(950, max(5, int(total * d["iam_role"]))),
        iam_policies=min(1400, max(5, int(total * d["iam_policy"]))),
        instance_profiles=max(2, int(total * d["instance_profile"])),
        kms_keys=max(1, int(total * d["kms_key"])),
        kms_aliases=max(1, int(total * d["kms_alias"])),
        secrets=max(2, int(total * d["secret"])),
        # Storage - S3 and DynamoDB only (no EFS - requires VPC)
        s3_buckets=max(5, int(total * d["s3_bucket"])),
        dynamodb_tables=min(2400, max(2, int(total * d["dynamodb_table"]))),
        efs_filesystems=0,
        efs_mount_targets=0,
        # Observability
        log_groups=max(5, int(total * d["log_group"])),
        sqs_queues=max(2, int(total * d["sqs_queue"])),
        # Application
        lambdas=max(2, int(total * d["lambda"])),
        ecr_repos=max(2, int(total * d["ecr_repo"])),
        # DNS & API Gateway - with caps
        route53_zones=min(450, max(1, int(total * d["route53_zone"]))),
        route53_records=max(5, int(total * d["route53_record"])),
        api_gateways=min(550, max(1, int(total * d["api_gateway"]))),
        api_stages=max(1, int(total * d["api_stage"])),
        # RDS - all zeroed out (requires VPC)
        rds_instances=0,
        db_subnet_groups=0,
        db_parameter_groups=0,
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


# ============================================================================
# Azure PKL Generators
# ============================================================================

AZURE_COPYRIGHT = '''/*
 * Stress Test Environment - Auto-generated (Azure)
 * Generated by generate-stress-test-env.py
 *
 * WARNING: This environment is for testing only. Resources will incur Azure costs.
 */
'''


def generate_azure_networking_pkl(counts: AzureResourceCounts, location: str) -> str:
    """Generate Azure networking.pkl with resource groups, VNets, subnets, NSGs, and public IPs."""

    rg_blocks = []
    vnet_blocks = []
    subnet_blocks = []
    nsg_blocks = []
    pip_blocks = []

    # Resource groups
    for i in range(counts.resource_groups):
        rg_blocks.append(f'''
  hidden rg{i}: rg.ResourceGroup = new {{
    label = "\\(prefix)-rg-{i}"
    name = "\\(prefix)-rg-{i}"
    location = "{location}"
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
      new azure.Tag {{ key = "run-id"; value = "\\(prefix)" }}
    }}
  }}''')

    # Virtual networks
    for i in range(counts.virtual_networks):
        rg_idx = i % counts.resource_groups
        vnet_blocks.append(f'''
  hidden vnet{i}: vnet.VirtualNetwork = new {{
    label = "\\(prefix)-vnet-{i}"
    name = "\\(prefix)-vnet-{i}"
    location = "{location}"
    resourceGroupName = rg{rg_idx}.res.name
    addressSpace = new vnet.AddressSpace {{
      addressPrefixes {{
        "10.{i % 256}.0.0/16"
      }}
    }}
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

        # Subnets for this VNet
        for j in range(counts.subnets_per_vnet):
            subnet_blocks.append(f'''
  hidden subnet{i}_{j}: snet.Subnet = new {{
    label = "\\(prefix)-subnet-{i}-{j}"
    name = "\\(prefix)-subnet-{i}-{j}"
    resourceGroupName = rg{rg_idx}.res.name
    virtualNetworkName = vnet{i}.res.name
    addressPrefix = "10.{i % 256}.{j}.0/24"
  }}''')

    # NSGs
    for i in range(counts.nsgs):
        rg_idx = i % counts.resource_groups
        nsg_blocks.append(f'''
  hidden nsg{i}: nsg.NetworkSecurityGroup = new {{
    label = "\\(prefix)-nsg-{i}"
    name = "\\(prefix)-nsg-{i}"
    location = "{location}"
    resourceGroupName = rg{rg_idx}.res.name
    securityRules {{
      new nsg.SecurityRule {{
        name = "AllowHTTP"
        priority = {100 + i}
        direction = "Inbound"
        access = "Allow"
        protocol = "Tcp"
        sourceAddressPrefix = "10.0.0.0/8"
        sourcePortRange = "*"
        destinationAddressPrefix = "*"
        destinationPortRange = "80"
      }}
    }}
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Public IPs
    for i in range(counts.public_ips):
        rg_idx = i % counts.resource_groups
        pip_blocks.append(f'''
  hidden pip{i}: pip.PublicIPAddress = new {{
    label = "\\(prefix)-pip-{i}"
    name = "\\(prefix)-pip-{i}"
    location = "{location}"
    resourceGroupName = rg{rg_idx}.res.name
    sku = new pip.PublicIPAddressSKU {{
      name = "Standard"
      tier = "Regional"
    }}
    publicIPAllocationMethod = "Static"
    publicIPAddressVersion = "IPv4"
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Resource listing
    resource_list = []
    for i in range(counts.resource_groups):
        resource_list.append(f"    rg{i}")
    for i in range(counts.virtual_networks):
        resource_list.append(f"    vnet{i}")
        for j in range(counts.subnets_per_vnet):
            resource_list.append(f"    subnet{i}_{j}")
    for i in range(counts.nsgs):
        resource_list.append(f"    nsg{i}")
    for i in range(counts.public_ips):
        resource_list.append(f"    pip{i}")

    return f'''{AZURE_COPYRIGHT}
import "@azure/azure.pkl"
import "@azure/resources/resourcegroup.pkl" as rg
import "@azure/resources/virtualnetwork.pkl" as vnet
import "@azure/resources/subnet.pkl" as snet
import "@azure/network/networksecuritygroup.pkl" as nsg
import "@azure/network/publicipaddress.pkl" as pip

class AzureNetworking {{
  prefix: String
{"".join(rg_blocks)}
{"".join(vnet_blocks)}
{"".join(subnet_blocks)}
{"".join(nsg_blocks)}
{"".join(pip_blocks)}

  // Expose resource groups for other modules
  resourceGroups: Listing = new {{
{chr(10).join(f"    rg{i}" for i in range(counts.resource_groups))}
  }}

  // Expose VNets for other modules
  vnets: Listing = new {{
{chr(10).join(f"    vnet{i}" for i in range(counts.virtual_networks))}
  }}

  // Expose subnets for other modules
  subnets: Listing = new {{
{chr(10).join(f"    subnet{i}_0" for i in range(counts.virtual_networks))}
  }}

  // Expose NSGs for other modules
  nsgs: Listing = new {{
{chr(10).join(f"    nsg{i}" for i in range(counts.nsgs))}
  }}

  // Expose public IPs for other modules
  publicIps: Listing = new {{
{chr(10).join(f"    pip{i}" for i in range(counts.public_ips))}
  }}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_azure_compute_pkl(counts: AzureResourceCounts, location: str) -> str:
    """Generate Azure compute.pkl with VMs and NICs."""

    nic_blocks = []
    vm_blocks = []

    # Network interfaces
    for i in range(counts.network_interfaces):
        rg_idx = i % counts.resource_groups
        vnet_idx = i % counts.virtual_networks
        nsg_idx = i % counts.nsgs
        pip_idx = i % counts.public_ips if i < counts.public_ips else -1

        ip_config = f'''
      new nic.IPConfiguration {{
        name = "ipconfig1"
        subnet = networking.subnets.toList()[{vnet_idx}].res.id
        privateIPAllocationMethod = "Dynamic"
        primary = true'''
        if pip_idx >= 0:
            ip_config += f'''
        publicIPAddress = networking.publicIps.toList()[{pip_idx}].res.id'''
        ip_config += '''
      }'''

        nic_blocks.append(f'''
  hidden nic{i}: nic.NetworkInterface = new {{
    label = "\\(prefix)-nic-{i}"
    name = "\\(prefix)-nic-{i}"
    location = "{location}"
    resourceGroupName = networking.resourceGroups.toList()[{rg_idx}].res.name
    ipConfigurations {{{ip_config}
    }}
    networkSecurityGroup = networking.nsgs.toList()[{nsg_idx}].res.id
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    # Virtual machines
    for i in range(counts.virtual_machines):
        rg_idx = i % counts.resource_groups
        nic_idx = i % counts.network_interfaces

        vm_blocks.append(f'''
  hidden vm{i}: vm.VirtualMachine = new {{
    label = "\\(prefix)-vm-{i}"
    name = "\\(prefix)-vm-{i}"
    location = "{location}"
    resourceGroupName = networking.resourceGroups.toList()[{rg_idx}].res.name
    vmSize = "Standard_B1s"
    networkInterfaces {{
      new vm.NetworkInterfaceReference {{
        id = nic{nic_idx}.res.id
        primary = true
      }}
    }}
    imageReference = new vm.ImageReference {{
      publisher = "Canonical"
      offer = "0001-com-ubuntu-server-jammy"
      sku = "22_04-lts-gen2"
      version = "latest"
    }}
    osDisk = new vm.OSDisk {{
      createOption = "FromImage"
      managedDisk = new vm.ManagedDiskParameters {{
        storageAccountType = "Standard_LRS"
      }}
      caching = "ReadWrite"
      deleteOption = "Delete"
    }}
    adminUsername = "azureadmin"
    linuxConfiguration = new vm.LinuxConfiguration {{
      disablePasswordAuthentication = true
      provisionVMAgent = true
      ssh = new vm.SSHConfiguration {{
        publicKeys {{
          new vm.SSHPublicKey {{
            path = "/home/azureadmin/.ssh/authorized_keys"
            keyData = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC+PLACEHOLDER stress-test-key"
          }}
        }}
      }}
    }}
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.network_interfaces):
        resource_list.append(f"    nic{i}")
    for i in range(counts.virtual_machines):
        resource_list.append(f"    vm{i}")

    return f'''{AZURE_COPYRIGHT}
import "@azure/azure.pkl"
import "@azure/network/networkinterface.pkl" as nic
import "@azure/compute/virtualmachine.pkl" as vm

import "./networking.pkl" as networkingModule

class AzureCompute {{
  prefix: String
  networking: networkingModule.AzureNetworking
{"".join(nic_blocks)}
{"".join(vm_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_azure_storage_pkl(counts: AzureResourceCounts, location: str) -> str:
    """Generate Azure storage.pkl with storage accounts."""

    sa_blocks = []
    for i in range(counts.storage_accounts):
        rg_idx = i % counts.resource_groups
        # Storage account names: lowercase, no hyphens, 3-24 chars
        sa_blocks.append(f'''
  hidden sa{i}: sa.StorageAccount = new {{
    label = "\\(prefix)-sa-{i}"
    name = "\\(safeName)sa{i}"
    location = "{location}"
    resourceGroupName = networking.resourceGroups.toList()[{rg_idx}].res.name
    kind = "StorageV2"
    sku = new sa.StorageAccountSKU {{ name = "Standard_LRS" }}
    enableHttpsTrafficOnly = true
    minimumTlsVersion = "TLS1_2"
    allowBlobPublicAccess = false
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.storage_accounts):
        resource_list.append(f"    sa{i}")

    return f'''{AZURE_COPYRIGHT}
import "@azure/azure.pkl"
import "@azure/storage/storageaccount.pkl" as sa

import "./networking.pkl" as networkingModule

class AzureStorage {{
  prefix: String
  // safeName: name with hyphens removed and truncated for storage account naming (max 24 chars)
  safeName: String
  networking: networkingModule.AzureNetworking
{"".join(sa_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_azure_iam_pkl(counts: AzureResourceCounts, location: str) -> str:
    """Generate Azure iam.pkl with managed identities."""

    mi_blocks = []
    for i in range(counts.managed_identities):
        rg_idx = i % counts.resource_groups
        mi_blocks.append(f'''
  hidden identity{i}: identity.UserAssignedIdentity = new {{
    label = "\\(prefix)-identity-{i}"
    name = "\\(prefix)-identity-{i}"
    location = "{location}"
    resourceGroupName = networking.resourceGroups.toList()[{rg_idx}].res.name
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.managed_identities):
        resource_list.append(f"    identity{i}")

    return f'''{AZURE_COPYRIGHT}
import "@azure/azure.pkl"
import "@azure/managedidentity/userassignedidentity.pkl" as identity

import "./networking.pkl" as networkingModule

class AzureIAM {{
  prefix: String
  networking: networkingModule.AzureNetworking
{"".join(mi_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_azure_security_pkl(counts: AzureResourceCounts, location: str) -> str:
    """Generate Azure security.pkl with key vaults and container registries."""

    kv_blocks = []
    cr_blocks = []

    for i in range(counts.key_vaults):
        rg_idx = i % counts.resource_groups
        kv_blocks.append(f'''
  hidden kv{i}: kv.Vault = new {{
    label = "\\(prefix)-kv-{i}"
    name = "\\(kvPrefix)-kv-{i}"
    location = "{location}"
    resourceGroupName = networking.resourceGroups.toList()[{rg_idx}].res.name
    tenantId = "00000000-0000-0000-0000-000000000000"
    sku = new kv.VaultSKU {{ family = "A"; name = "standard" }}
    enableSoftDelete = false
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    for i in range(counts.container_registries):
        rg_idx = i % counts.resource_groups
        # Registry names: alphanumeric only, 5-50 chars
        cr_blocks.append(f'''
  hidden cr{i}: cr.Registry = new {{
    label = "\\(prefix)-cr-{i}"
    name = "\\(safeName)cr{i}"
    location = "{location}"
    resourceGroupName = networking.resourceGroups.toList()[{rg_idx}].res.name
    sku = new cr.RegistrySKU {{ name = "Basic" }}
    adminUserEnabled = false
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.key_vaults):
        resource_list.append(f"    kv{i}")
    for i in range(counts.container_registries):
        resource_list.append(f"    cr{i}")

    return f'''{AZURE_COPYRIGHT}
import "@azure/azure.pkl"
import "@azure/keyvault/vault.pkl" as kv
import "@azure/containerregistry/registry.pkl" as cr

import "./networking.pkl" as networkingModule

class AzureSecurity {{
  prefix: String
  // kvPrefix: truncated name for key vault naming (max 24 chars total with suffix)
  kvPrefix: String
  // safeName: name with hyphens removed for registry naming
  safeName: String
  networking: networkingModule.AzureNetworking
{"".join(kv_blocks)}
{"".join(cr_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_azure_database_pkl(counts: AzureResourceCounts, location: str) -> str:
    """Generate Azure database.pkl with PostgreSQL flexible servers."""

    pg_blocks = []
    for i in range(counts.postgres_servers):
        rg_idx = i % counts.resource_groups
        pg_blocks.append(f'''
  hidden pg{i}: pg.FlexibleServer = new {{
    label = "\\(prefix)-pg-{i}"
    name = "\\(prefix)-pg-{i}"
    location = "{location}"
    resourceGroupName = networking.resourceGroups.toList()[{rg_idx}].res.name
    sku = new pg.SKU {{ name = "Standard_B1ms"; tier = "Burstable" }}
    version = "15"
    administratorLogin = "pgadmin"
    administratorLoginPassword = "StressTest123!"
    storage = new pg.Storage {{ storageSizeGB = 32; autoGrow = "Disabled" }}
    backup = new pg.Backup {{ backupRetentionDays = 7; geoRedundantBackup = "Disabled" }}
    highAvailability = new pg.HighAvailability {{ mode = "Disabled" }}
    authConfig = new pg.AuthConfig {{ passwordAuth = "Enabled"; activeDirectoryAuth = "Disabled" }}
    tags {{
      new azure.Tag {{ key = "Environment"; value = "stress-test" }}
      new azure.Tag {{ key = "ManagedBy"; value = "formae" }}
    }}
  }}''')

    resource_list = []
    for i in range(counts.postgres_servers):
        resource_list.append(f"    pg{i}")

    return f'''{AZURE_COPYRIGHT}
import "@azure/azure.pkl"
import "@azure/dbforpostgresql/flexibleserver.pkl" as pg

import "./networking.pkl" as networkingModule

class AzureDatabase {{
  prefix: String
  networking: networkingModule.AzureNetworking
{"".join(pg_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_azure_main_pkl(env_id: str, counts: AzureResourceCounts) -> str:
    """Generate azure/main.pkl that ties Azure resources together."""

    has_compute = counts.virtual_machines > 0 or counts.network_interfaces > 0
    has_database = counts.postgres_servers > 0

    imports = [
        'import "./networking.pkl" as networkingModule',
        'import "./storage.pkl" as storageModule',
        'import "./iam.pkl" as iamModule',
        'import "./security.pkl" as securityModule',
    ]
    if has_compute:
        imports.append('import "./compute.pkl" as computeModule')
    if has_database:
        imports.append('import "./database.pkl" as databaseModule')

    locals_block = f'''
local _networking = new networkingModule.AzureNetworking {{
    prefix = vars.envId
}}

local _storage = new storageModule.AzureStorage {{
    prefix = vars.envId
    safeName = vars.azureSafeName
    networking = _networking
}}

local _iam = new iamModule.AzureIAM {{
    prefix = vars.envId
    networking = _networking
}}

local _security = new securityModule.AzureSecurity {{
    prefix = vars.envId
    kvPrefix = vars.azureKvPrefix
    safeName = vars.azureSafeName
    networking = _networking
}}'''

    if has_compute:
        locals_block += f'''

local _compute = new computeModule.AzureCompute {{
    prefix = vars.envId
    networking = _networking
}}'''

    if has_database:
        locals_block += f'''

local _database = new databaseModule.AzureDatabase {{
    prefix = vars.envId
    networking = _networking
}}'''

    spread_items = [
        '    ..._networking.resources',
        '    ..._storage.resources',
        '    ..._iam.resources',
        '    ..._security.resources',
    ]
    if has_compute:
        spread_items.append('    ..._compute.resources')
    if has_database:
        spread_items.append('    ..._database.resources')

    return f'''{AZURE_COPYRIGHT}
amends "@formae/forma.pkl"
import "@formae/formae.pkl"

import "../vars.pkl"
{chr(10).join(imports)}
{locals_block}

forma {{
    vars.azureStack
    vars.azureTarget

{chr(10).join(spread_items)}
}}
'''


# ============================================================================
# GCP PKL Generators
# ============================================================================

GCP_COPYRIGHT = '''/*
 * Stress Test Environment - Auto-generated (GCP)
 * Generated by generate-stress-test-env.py
 *
 * WARNING: This environment is for testing only. Resources will incur GCP costs.
 */
'''


def generate_gcp_networking_pkl(counts: GCPResourceCounts, region: str) -> str:
    """Generate GCP networking.pkl with networks, subnetworks, firewalls, and addresses."""

    network_blocks = []
    subnet_blocks = []
    firewall_blocks = []
    address_blocks = []

    # Networks
    for i in range(counts.compute_networks):
        network_blocks.append(f'''
  hidden net{i}: network.Network = new {{
    label = "\\(prefix)-net-{i}"
    name = "\\(prefix)-net-{i}"
    autoCreateSubnetworks = false
    routingConfig = new network.RoutingConfig {{
      routingMode = "REGIONAL"
    }}
  }}''')

    # Subnetworks
    for i in range(counts.compute_subnetworks):
        net_idx = i % counts.compute_networks
        subnet_blocks.append(f'''
  hidden subnet{i}: subnetwork.Subnetwork = new {{
    label = "\\(prefix)-subnet-{i}"
    name = "\\(prefix)-subnet-{i}"
    network = net{net_idx}.res.selfLink
    region = "{region}"
    ipCidrRange = "10.{i // 256}.{i % 256}.0/24"
    privateIpGoogleAccess = true
  }}''')

    # Firewalls
    protocols = ["tcp", "udp", "tcp"]
    ports = [["80", "443"], ["53"], ["22"]]
    for i in range(counts.compute_firewalls):
        net_idx = i % counts.compute_networks
        proto = protocols[i % len(protocols)]
        port_list = ports[i % len(ports)]
        port_entries = "; ".join(f'"{p}"' for p in port_list)
        firewall_blocks.append(f'''
  hidden fw{i}: firewall.Firewall = new {{
    label = "\\(prefix)-fw-{i}"
    name = "\\(prefix)-fw-{i}"
    network = net{net_idx}.res.selfLink
    direction = "INGRESS"
    sourceRanges {{
      "10.0.0.0/8"
    }}
    allowed {{
      new {{
        protocol = "{proto}"
        ports {{ {port_entries} }}
      }}
    }}
  }}''')

    # Addresses
    for i in range(counts.compute_addresses):
        address_blocks.append(f'''
  hidden addr{i}: address.Address = new {{
    label = "\\(prefix)-addr-{i}"
    name = "\\(prefix)-addr-{i}"
    region = "{region}"
    addressType = "EXTERNAL"
  }}''')

    resource_list = []
    for i in range(counts.compute_networks):
        resource_list.append(f"    net{i}")
    for i in range(counts.compute_subnetworks):
        resource_list.append(f"    subnet{i}")
    for i in range(counts.compute_firewalls):
        resource_list.append(f"    fw{i}")
    for i in range(counts.compute_addresses):
        resource_list.append(f"    addr{i}")

    return f'''{GCP_COPYRIGHT}
import "@gcp/compute/network.pkl"
import "@gcp/compute/subnetwork.pkl"
import "@gcp/compute/firewall.pkl"
import "@gcp/compute/address.pkl"

class GCPNetworking {{
  prefix: String
{"".join(network_blocks)}
{"".join(subnet_blocks)}
{"".join(firewall_blocks)}
{"".join(address_blocks)}

  // Expose networks for other modules
  networks: Listing = new {{
{chr(10).join(f"    net{i}" for i in range(counts.compute_networks))}
  }}

  // Expose subnets for other modules
  subnets: Listing = new {{
{chr(10).join(f"    subnet{i}" for i in range(counts.compute_subnetworks))}
  }}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_gcp_compute_pkl(counts: GCPResourceCounts, region: str, project: str) -> str:
    """Generate GCP compute.pkl with instances and disks."""

    disk_blocks = []
    instance_blocks = []

    zone = f"{region}-b"

    # Disks
    for i in range(counts.compute_disks):
        disk_blocks.append(f'''
  hidden disk{i}: disk.Disk = new {{
    label = "\\(prefix)-disk-{i}"
    project = "{project}"
    zone = "{zone}"
    name = "\\(prefix)-disk-{i}"
    sizeGb = {20 + (i % 5) * 10}
    type = "{["pd-balanced", "pd-standard"][i % 2]}"
    labels {{
      ["environment"] = "stress-test"
      ["managed-by"] = "formae"
    }}
  }}''')

    # Instances
    machine_types = ["e2-micro", "e2-small"]
    for i in range(counts.compute_instances):
        net_idx = i % counts.compute_networks
        subnet_idx = i % max(1, counts.compute_subnetworks)
        machine_type = machine_types[i % len(machine_types)]

        instance_blocks.append(f'''
  hidden instance{i}: instance.Instance = new {{
    label = "\\(prefix)-instance-{i}"
    project = "{project}"
    zone = "{zone}"
    name = "\\(prefix)-instance-{i}"
    machineType = "{machine_type}"
    networkInterfaces {{
      new {{
        network = networking.networks.toList()[{net_idx}].res.selfLink
        subnetwork = networking.subnets.toList()[{subnet_idx}].res.selfLink
      }}
    }}
    labels {{
      ["environment"] = "stress-test"
      ["managed-by"] = "formae"
    }}
  }}''')
        # NOTE: Boot disk via initializeParams is not yet supported in the GCP plugin.
        # Instances are created without explicit boot disks for now.

    resource_list = []
    for i in range(counts.compute_disks):
        resource_list.append(f"    disk{i}")
    for i in range(counts.compute_instances):
        resource_list.append(f"    instance{i}")

    return f'''{GCP_COPYRIGHT}
import "@gcp/compute/disk.pkl"
import "@gcp/compute/instance.pkl"

import "./networking.pkl" as networkingModule

class GCPCompute {{
  prefix: String
  networking: networkingModule.GCPNetworking
{"".join(disk_blocks)}
{"".join(instance_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_gcp_storage_pkl(counts: GCPResourceCounts, region: str) -> str:
    """Generate GCP storage.pkl with storage buckets."""

    bucket_blocks = []
    for i in range(counts.storage_buckets):
        bucket_blocks.append(f'''
  hidden bucket{i}: bucket.Bucket = new {{
    label = "\\(prefix)-bucket-{i}"
    name = "\\(prefix)-bucket-{i}"
    location = "{region}"
    storageClass = "STANDARD"
    versioning = new bucket.Versioning {{ enabled = true }}
  }}''')

    resource_list = []
    for i in range(counts.storage_buckets):
        resource_list.append(f"    bucket{i}")

    return f'''{GCP_COPYRIGHT}
import "@gcp/storage/bucket.pkl"

class GCPStorage {{
  prefix: String
{"".join(bucket_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_gcp_database_pkl(counts: GCPResourceCounts, region: str, project: str) -> str:
    """Generate GCP database.pkl with Cloud SQL instances and BigQuery datasets."""

    sql_blocks = []
    bq_blocks = []

    for i in range(counts.sql_instances):
        sql_blocks.append(f'''
  hidden sqlInstance{i}: database.DatabaseInstance = new {{
    label = "\\(prefix)-sql-{i}"
    project = "{project}"
    name = "\\(prefix)-sql-{i}"
    databaseVersion = "POSTGRES_15"
    region = "{region}"
    settings = new database.Settings {{
      tier = "db-f1-micro"
      availabilityType = "ZONAL"
      dataDiskSizeGb = 10
      dataDiskType = "PD_SSD"
      ipConfiguration = new database.IpConfiguration {{
        ipv4Enabled = true
        requireSsl = false
      }}
      userLabels = new Mapping {{
        ["environment"] = "stress-test"
        ["managed-by"] = "formae"
      }}
    }}
  }}''')

    for i in range(counts.bigquery_datasets):
        bq_blocks.append(f'''
  hidden bqDataset{i}: dataset.Dataset = new {{
    label = "\\(prefix)-bq-{i}"
    project = "{project}"
    datasetId = "\\(bqSafeName)_bq_{i}"
    location = "{region}"
    description = "Stress test dataset {i}"
  }}''')

    resource_list = []
    for i in range(counts.sql_instances):
        resource_list.append(f"    sqlInstance{i}")
    for i in range(counts.bigquery_datasets):
        resource_list.append(f"    bqDataset{i}")

    return f'''{GCP_COPYRIGHT}
import "@gcp/sql/database.pkl"
import "@gcp/bigquery/dataset.pkl"

class GCPDatabase {{
  prefix: String
  // bqSafeName: name with hyphens replaced by underscores for BigQuery dataset IDs
  bqSafeName: String
{"".join(sql_blocks)}
{"".join(bq_blocks)}

  resources: Listing = new {{
{chr(10).join(resource_list)}
  }}
}}
'''


def generate_gcp_main_pkl(env_id: str, counts: GCPResourceCounts) -> str:
    """Generate gcp/main.pkl that ties GCP resources together."""

    has_compute = counts.compute_instances > 0 or counts.compute_disks > 0
    has_database = counts.sql_instances > 0 or counts.bigquery_datasets > 0

    imports = [
        'import "./networking.pkl" as networkingModule',
        'import "./storage.pkl" as storageModule',
    ]
    if has_compute:
        imports.append('import "./compute.pkl" as computeModule')
    if has_database:
        imports.append('import "./database.pkl" as databaseModule')

    locals_block = f'''
local _networking = new networkingModule.GCPNetworking {{
    prefix = vars.envId
}}

local _storage = new storageModule.GCPStorage {{
    prefix = vars.envId
}}'''

    if has_compute:
        locals_block += f'''

local _compute = new computeModule.GCPCompute {{
    prefix = vars.envId
    networking = _networking
}}'''

    if has_database:
        locals_block += f'''

local _database = new databaseModule.GCPDatabase {{
    prefix = vars.envId
    bqSafeName = vars.gcpBqSafeName
}}'''

    spread_items = [
        '    ..._networking.resources',
        '    ..._storage.resources',
    ]
    if has_compute:
        spread_items.append('    ..._compute.resources')
    if has_database:
        spread_items.append('    ..._database.resources')

    return f'''{GCP_COPYRIGHT}
amends "@formae/forma.pkl"
import "@formae/formae.pkl"

import "../vars.pkl"
{chr(10).join(imports)}
{locals_block}

forma {{
    vars.gcpStack
    vars.gcpTarget

{chr(10).join(spread_items)}
}}
'''


# ============================================================================
# Multi-cloud vars.pkl and main.pkl generators
# ============================================================================


def generate_multicloud_vars_pkl(
    env_id: str,
    run_id: str,
    clouds: list,
    region: str,
    stack_name: str,
    azure_subscription: str = None,
    azure_location: str = "eastus",
    gcp_project: str = None,
    gcp_region: str = "us-central1",
) -> str:
    """Generate vars.pkl with environment configuration for all enabled clouds."""

    content = f'''{COPYRIGHT}
import "@formae/formae.pkl"
'''

    if "aws" in clouds:
        content += 'import "@aws/aws.pkl"\n'
    if "azure" in clouds:
        content += 'import "@azure/azure.pkl"\n'
    if "gcp" in clouds:
        content += 'import "@gcp/gcp.pkl"\n'

    content += f'''
envId = "{env_id}"
runId = "{run_id}"
'''

    if "aws" in clouds:
        content += f'''
// AWS Configuration
awsRegion = "{region}"

awsStack: formae.Stack = new {{
    label = "{stack_name}-aws"
    description = "Stress test environment {env_id} - AWS"
}}

awsTarget: formae.Target = new {{
    label = "stress-test-aws"
    config = new aws.Config {{
        region = awsRegion
    }}
}}

// Helper for unique naming
local function uniqueName(base: String): String = "\\(envId)-\\(base)"
'''

    if "azure" in clouds:
        content += f'''
// Azure Configuration
azureLocation = "{azure_location}"
azureSubscriptionId = "{azure_subscription}"

// Safe name for Azure resources that don't allow hyphens (storage accounts, registries)
azureSafeName = envId.replaceAll("-", "").take(14)
// Prefix for key vault names (max 24 chars total with suffix)
azureKvPrefix = envId.take(16)

azureStack: formae.Stack = new {{
    label = "{stack_name}-azure"
    description = "Stress test environment {env_id} - Azure"
}}

azureTarget: formae.Target = new {{
    label = "stress-test-azure"
    config = new azure.Config {{
        subscriptionId = azureSubscriptionId
    }}
}}
'''

    if "gcp" in clouds:
        content += f'''
// GCP Configuration
gcpProject = "{gcp_project}"
gcpRegion = "{gcp_region}"

// Safe name for BigQuery dataset IDs (no hyphens allowed)
gcpBqSafeName = envId.replaceAll("-", "_")

gcpStack: formae.Stack = new {{
    label = "{stack_name}-gcp"
    description = "Stress test environment {env_id} - GCP"
}}

gcpTarget: formae.Target = new {{
    label = "stress-test-gcp"
    config = new gcp.Config {{
        project = gcpProject
        region = gcpRegion
        location = gcpRegion
    }}
}}
'''

    return content


def generate_multicloud_main_pkl(
    env_id: str,
    clouds: list,
    aws_counts: ResourceCounts = None,
    azure_counts: AzureResourceCounts = None,
    gcp_counts: GCPResourceCounts = None,
) -> str:
    """Generate main.pkl that imports all cloud-specific files."""

    imports = ['import "./vars.pkl"']

    if "aws" in clouds:
        imports.append('import "./aws/main.pkl" as awsForma')
    if "azure" in clouds:
        imports.append('import "./azure/main.pkl" as azureForma')
    if "gcp" in clouds:
        imports.append('import "./gcp/main.pkl" as gcpForma')

    # Build description
    total = 0
    cloud_details = []
    if "aws" in clouds and aws_counts:
        total += aws_counts.total
        cloud_details.append(f"    - AWS: ~{aws_counts.total} resources")
    if "azure" in clouds and azure_counts:
        total += azure_counts.total
        cloud_details.append(f"    - Azure: ~{azure_counts.total} resources")
    if "gcp" in clouds and gcp_counts:
        total += gcp_counts.total
        cloud_details.append(f"    - GCP: ~{gcp_counts.total} resources")

    # Each cloud has its own main.pkl that is a standalone forma file.
    # Apply each one separately: formae apply aws/main.pkl, azure/main.pkl, gcp/main.pkl
    # This is a documentation/index file listing what was generated.
    cloud_paths = []
    if "aws" in clouds:
        cloud_paths.append("aws/main.pkl")
    if "azure" in clouds:
        cloud_paths.append("azure/main.pkl")
    if "gcp" in clouds:
        cloud_paths.append("gcp/main.pkl")

    apply_cmds = chr(10).join(f"#   formae apply {p} --yes" for p in cloud_paths)
    destroy_cmds = chr(10).join(f"#   formae destroy {p} --yes" for p in cloud_paths)

    return f'''# Multi-Cloud Stress Test Environment: {env_id}
#
# This environment contains approximately {total} resources across
# {", ".join(c.upper() for c in clouds)}.
#
{chr(10).join(cloud_details)}
#
# Each cloud has its own forma file. Apply them separately:
#
{apply_cmds}
#
# To destroy:
#
{destroy_cmds}
'''


def generate_multicloud_pkl_project(formae_path: str, clouds: list) -> str:
    """Generate PklProject file for multi-cloud environments."""
    # Plugin repos live alongside the formae repo as siblings
    parent_dir = os.path.dirname(formae_path)
    deps = [f'  ["formae"] = import("{formae_path}/internal/schema/pkl/schema/PklProject")']

    if "aws" in clouds:
        deps.append(f'  ["aws"] = import("{parent_dir}/formae-plugin-aws/schema/pkl/PklProject")')
    if "azure" in clouds:
        deps.append(f'  ["azure"] = import("{parent_dir}/formae-plugin-azure/schema/pkl/PklProject")')
    if "gcp" in clouds:
        deps.append(f'  ["gcp"] = import("{parent_dir}/formae-plugin-gcp/schema/pkl/PklProject")')

    return f'''amends "pkl:Project"

dependencies {{
{chr(10).join(deps)}
}}
'''


def generate_main_pkl(env_id: str, stack_name: str, counts: ResourceCounts,
                      stack_var: str = "stack", target_var: str = "target",
                      region_var: str = "_region") -> str:
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
local _region = vars.{region_var}

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
    vars.{stack_var}
    vars.{target_var}

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
    parent_dir = os.path.dirname(formae_path)
    return f'''amends "pkl:Project"

dependencies {{
  ["formae"] = import("{formae_path}/internal/schema/pkl/schema/PklProject")
  ["aws"] = import("{parent_dir}/formae-plugin-aws/schema/pkl/PklProject")
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


def split_into_chunks(items: list, chunk_size: int) -> List[Tuple[int, list]]:
    """Split a list into chunks and return (chunk_index, chunk_items) tuples."""
    chunks = []
    for i in range(0, len(items), chunk_size):
        chunk_idx = i // chunk_size
        chunk_items = items[i:i + chunk_size]
        chunks.append((chunk_idx, chunk_items))
    return chunks


def count_cfn_resources_in_template(template: str) -> int:
    """Count the number of resources in a CloudFormation template."""
    # Simple heuristic: count lines that define a resource type
    count = 0
    lines = template.split('\n')
    for i, line in enumerate(lines):
        if line.strip().startswith('Type: AWS::'):
            count += 1
    return count


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
    """Generate CloudFormation template for security groups (legacy single template)."""
    templates = generate_cfn_security_groups_split(counts, env_id, networking_stack)
    if len(templates) == 1:
        return templates[0][1]
    return templates[0][1]


def generate_cfn_security_groups_split(counts: ResourceCounts, env_id: str, networking_stack: str) -> List[Tuple[str, str]]:
    """Generate CloudFormation templates for security groups, splitting into multiple stacks if needed.

    Security groups are split with their corresponding rules to avoid cross-stack !GetAtt references.
    """
    templates = []

    # Calculate how many SGs per stack (to stay under MAX_RESOURCES_PER_STACK including rules)
    # Each SG has ~3 rules on average (ingress_rules/sgs + egress_rules/sgs)
    avg_rules_per_sg = ((counts.sg_ingress_rules + counts.sg_egress_rules) / max(1, counts.security_groups)) + 1
    sgs_per_stack = int(MAX_RESOURCES_PER_STACK / avg_rules_per_sg)

    if counts.security_groups <= sgs_per_stack:
        # Single stack
        template = CFN_HEADER.format(description=f"Security groups stack for perf test {env_id}")
        template += "Resources:\n"
        outputs = []

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
        templates.append(("security-groups.yaml", template))
    else:
        # Split into multiple stacks - each stack has a contiguous range of SGs with their rules
        num_stacks = (counts.security_groups + sgs_per_stack - 1) // sgs_per_stack

        for stack_idx in range(num_stacks):
            sg_start = stack_idx * sgs_per_stack
            sg_end = min(sg_start + sgs_per_stack, counts.security_groups)

            template = CFN_HEADER.format(description=f"Security groups stack {stack_idx} for perf test {env_id}")
            template += "Resources:\n"
            outputs = []

            # Security groups for this stack
            for i in range(sg_start, sg_end):
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

            # Ingress rules for SGs in this stack
            for i in range(counts.sg_ingress_rules):
                sg_idx = i % counts.security_groups
                if sg_start <= sg_idx < sg_end:
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

            # Egress rules for SGs in this stack
            for i in range(counts.sg_egress_rules):
                sg_idx = i % counts.security_groups
                if sg_start <= sg_idx < sg_end:
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
            templates.append((f"security-groups-{stack_idx}.yaml", template))

    return templates


def generate_cfn_iam(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for IAM resources (legacy single template)."""
    templates = generate_cfn_iam_split(counts, env_id)
    if len(templates) == 1:
        return templates[0][1]
    return templates[0][1]


def generate_cfn_iam_split(counts: ResourceCounts, env_id: str) -> List[Tuple[str, str]]:
    """Generate CloudFormation templates for IAM resources, splitting into multiple stacks if needed."""
    templates = []
    services = ["ec2.amazonaws.com", "ecs-tasks.amazonaws.com", "sagemaker.amazonaws.com", "states.amazonaws.com"]

    # Calculate Lambda roles count same as PKL
    min_other_roles = 1 if (counts.iam_policies > 0 or counts.instance_profiles > 0) else 0
    lambda_roles_count = min(
        max(5, int(counts.iam_roles * 0.2)),
        counts.iam_roles - min_other_roles
    )
    other_roles_count = counts.iam_roles - lambda_roles_count

    # Collect all IAM resources as (type, index, content, output_line)
    all_resources = []

    # Lambda execution roles
    for i in range(lambda_roles_count):
        content = f"""
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
        output = f"  LambdaRole{i}Arn:\n    Value: !GetAtt LambdaRole{i}.Arn\n    Export:\n      Name: !Sub '${{AWS::StackName}}-LambdaRole{i}Arn'"
        all_resources.append(('lambda_role', i, content, output))

    # Other IAM roles
    for i in range(other_roles_count):
        service = services[i % len(services)]
        content = f"""
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
        all_resources.append(('role', i, content, None))

    # IAM Policies
    for i in range(counts.iam_policies):
        content = f"""
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
        all_resources.append(('policy', i, content, None))

    # Instance profiles
    for i in range(counts.instance_profiles):
        content = f"""
  InstanceProfile{i}:
    Type: AWS::IAM::InstanceProfile
    Properties:
      InstanceProfileName: {env_id}-instance-profile-{i}
      Roles: []
"""
        all_resources.append(('instance_profile', i, content, None))

    # Split into stacks
    total_resources = len(all_resources)
    if total_resources <= MAX_RESOURCES_PER_STACK:
        # Single stack
        template = CFN_HEADER.format(description=f"IAM stack for perf test {env_id}")
        template += "Resources:\n"
        outputs = []
        for _, _, content, output in all_resources:
            template += content
            if output:
                outputs.append(output)
        if outputs:
            template += "\nOutputs:\n" + "\n".join(outputs)
        templates.append(("iam.yaml", template))
    else:
        # Split into multiple stacks
        chunks = split_into_chunks(all_resources, MAX_RESOURCES_PER_STACK)
        for chunk_idx, chunk_resources in chunks:
            template = CFN_HEADER.format(description=f"IAM stack {chunk_idx} for perf test {env_id}")
            template += "Resources:\n"
            outputs = []
            for _, _, content, output in chunk_resources:
                template += content
                if output:
                    outputs.append(output)
            if outputs:
                template += "\nOutputs:\n" + "\n".join(outputs)
            templates.append((f"iam-{chunk_idx}.yaml", template))

    return templates


def generate_cfn_storage(counts: ResourceCounts, env_id: str, networking_stack: str, sg_stack: str) -> str:
    """Generate CloudFormation template for storage resources (legacy single template)."""
    templates = generate_cfn_storage_split(counts, env_id, networking_stack, sg_stack)
    if len(templates) == 1:
        return templates[0][1]
    # For backwards compatibility, return first template (but this shouldn't be used for large counts)
    return templates[0][1]


def generate_cfn_storage_split(counts: ResourceCounts, env_id: str, networking_stack: str, sg_stack: str) -> List[Tuple[str, str]]:
    """Generate CloudFormation templates for storage resources, splitting into multiple stacks if needed.

    Returns a list of (filename, content) tuples.
    """
    templates = []

    # Collect all storage resources
    all_resources = []

    # S3 Buckets
    for i in range(counts.s3_buckets):
        all_resources.append(('bucket', i, f"""
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
"""))

    # DynamoDB Tables
    for i in range(counts.dynamodb_tables):
        all_resources.append(('dynamo', i, f"""
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
"""))

    # EFS File Systems and Mount Targets (keep together due to !Ref dependency)
    for i in range(counts.efs_filesystems):
        vpc_idx = i % counts.vpcs
        private_subnet_idx = counts.subnets_per_vpc // 2
        efs_content = f"""
  EFS{i}:
    Type: AWS::EFS::FileSystem
    Properties:
      PerformanceMode: generalPurpose
      Encrypted: true
      FileSystemTags:
{cfn_tags(f'{env_id}-efs-{i}')}
"""
        if i < counts.efs_mount_targets:
            efs_content += f"""
  EFSMountTarget{i}:
    Type: AWS::EFS::MountTarget
    Properties:
      FileSystemId: !Ref EFS{i}
      SubnetId: !ImportValue {networking_stack}-Subnet{vpc_idx}x{private_subnet_idx}Id
      SecurityGroups:
        - !ImportValue {sg_stack}-SG0Id
"""
        # EFS counts as 2 resources if it has a mount target
        all_resources.append(('efs', i, efs_content))

    # Split into chunks
    total_resources = len(all_resources)
    if total_resources <= MAX_RESOURCES_PER_STACK:
        # Single template
        template = CFN_HEADER.format(description=f"Storage stack for perf test {env_id}")
        template += "Resources:\n"
        for _, _, content in all_resources:
            template += content
        templates.append(("storage.yaml", template))
    else:
        # Split into multiple templates
        chunks = split_into_chunks(all_resources, MAX_RESOURCES_PER_STACK)
        for chunk_idx, chunk_resources in chunks:
            template = CFN_HEADER.format(description=f"Storage stack {chunk_idx} for perf test {env_id}")
            template += "Resources:\n"
            for _, _, content in chunk_resources:
                template += content
            templates.append((f"storage-{chunk_idx}.yaml", template))

    return templates


def generate_cfn_compute(counts: ResourceCounts, env_id: str, region: str, networking_stack: str, sg_stack_base: str) -> str:
    """Generate CloudFormation template for compute resources (legacy single template)."""
    templates = generate_cfn_compute_split(counts, env_id, region, networking_stack, sg_stack_base)
    if len(templates) == 1:
        return templates[0][1]
    return templates[0][1]


def generate_cfn_compute_split(counts: ResourceCounts, env_id: str, region: str, networking_stack: str, sg_stack_base: str) -> List[Tuple[str, str]]:
    """Generate CloudFormation templates for compute resources, splitting if needed."""
    templates = []

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

    # Determine if SGs are split
    total_sg_resources = counts.security_groups + counts.sg_ingress_rules + counts.sg_egress_rules
    avg_rules_per_sg = ((counts.sg_ingress_rules + counts.sg_egress_rules) / max(1, counts.security_groups)) + 1
    sgs_per_stack = int(MAX_RESOURCES_PER_STACK / avg_rules_per_sg)
    sg_is_split = counts.security_groups > sgs_per_stack

    all_resources = []

    # Launch templates
    for i in range(counts.launch_templates):
        content = f"""
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
        all_resources.append(('launch_template', i, content))

    # EC2 Instances
    instance_types = ["t3.micro", "t3.small", "t3.medium"]
    private_subnet_idx = counts.subnets_per_vpc // 2
    for i in range(counts.ec2_instances):
        vpc_idx = i % counts.vpcs
        sg_idx = i % counts.security_groups
        instance_type = instance_types[i % len(instance_types)]

        # Calculate which SG stack the security group is in
        if sg_is_split:
            sg_stack_idx = sg_idx // sgs_per_stack
            sg_stack = f"{sg_stack_base}-{sg_stack_idx}"
        else:
            sg_stack = sg_stack_base

        content = f"""
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
        all_resources.append(('instance', i, content))

    # EBS Volumes
    azs = ["a", "b", "c"]
    private_subnet_az = azs[private_subnet_idx % 3]

    standalone_volumes = counts.ebs_volumes // 2
    for i in range(counts.ebs_volumes):
        if i < standalone_volumes:
            az_suffix = azs[i % 3]
        else:
            az_suffix = private_subnet_az

        vol_type = ["gp3", "gp2"][i % 2]
        encrypted = str(i % 2 == 0).lower()

        content = f"""
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
        all_resources.append(('volume', i, content))

    # Split into stacks
    if len(all_resources) <= MAX_RESOURCES_PER_STACK:
        template = CFN_HEADER.format(description=f"Compute stack for perf test {env_id}")
        template += "Resources:\n"
        for _, _, content in all_resources:
            template += content
        templates.append(("compute.yaml", template))
    else:
        chunks = split_into_chunks(all_resources, MAX_RESOURCES_PER_STACK)
        for chunk_idx, chunk_resources in chunks:
            template = CFN_HEADER.format(description=f"Compute stack {chunk_idx} for perf test {env_id}")
            template += "Resources:\n"
            for _, _, content in chunk_resources:
                template += content
            templates.append((f"compute-{chunk_idx}.yaml", template))

    return templates


def generate_cfn_observability(counts: ResourceCounts, env_id: str) -> str:
    """Generate CloudFormation template for observability resources (legacy single template)."""
    templates = generate_cfn_observability_split(counts, env_id)
    if len(templates) == 1:
        return templates[0][1]
    return templates[0][1]


def generate_cfn_observability_split(counts: ResourceCounts, env_id: str) -> List[Tuple[str, str]]:
    """Generate CloudFormation templates for observability resources, splitting if needed."""
    templates = []
    all_resources = []

    # CloudWatch Log Groups
    for i in range(counts.log_groups):
        content = f"""
  LogGroup{i}:
    Type: AWS::Logs::LogGroup
    Properties:
      LogGroupName: /perf-test/{env_id}/app-{i}
      RetentionInDays: 7
      Tags:
{cfn_tags(f'{env_id}-log-group-{i}')}
"""
        all_resources.append(('log_group', i, content))

    # SQS Queues
    for i in range(counts.sqs_queues):
        content = f"""
  SQSQueue{i}:
    Type: AWS::SQS::Queue
    Properties:
      QueueName: {env_id}-queue-{i}
      VisibilityTimeout: 30
      MessageRetentionPeriod: 345600
      Tags:
{cfn_tags(f'{env_id}-sqs-queue-{i}')}
"""
        all_resources.append(('sqs_queue', i, content))

    # Split into stacks
    if len(all_resources) <= MAX_RESOURCES_PER_STACK:
        template = CFN_HEADER.format(description=f"Observability stack for perf test {env_id}")
        template += "Resources:\n"
        for _, _, content in all_resources:
            template += content
        templates.append(("observability.yaml", template))
    else:
        chunks = split_into_chunks(all_resources, MAX_RESOURCES_PER_STACK)
        for chunk_idx, chunk_resources in chunks:
            template = CFN_HEADER.format(description=f"Observability stack {chunk_idx} for perf test {env_id}")
            template += "Resources:\n"
            for _, _, content in chunk_resources:
                template += content
            templates.append((f"observability-{chunk_idx}.yaml", template))

    return templates


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
    """Generate CloudFormation template for KMS resources (legacy single template)."""
    templates = generate_cfn_kms_split(counts, env_id)
    if len(templates) == 1:
        return templates[0][1]
    return templates[0][1]


def generate_cfn_kms_split(counts: ResourceCounts, env_id: str) -> List[Tuple[str, str]]:
    """Generate CloudFormation templates for KMS resources, splitting if needed.

    Keys and their aliases are kept together since aliases reference keys via !Ref.
    Each key+alias pair counts as 2 resources.
    """
    templates = []
    all_resources = []

    # Each key+alias pair is kept together
    for i in range(counts.kms_keys):
        key_content = f"""
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
        # Add alias if we have one for this key
        if i < counts.kms_aliases:
            key_content += f"""
  KMSAlias{i}:
    Type: AWS::KMS::Alias
    Properties:
      AliasName: alias/{env_id}-key-{i}
      TargetKeyId: !Ref KMSKey{i}
"""
        all_resources.append(('kms', i, key_content))

    # Each key+alias pair counts as 2 resources, so use 200 pairs per stack (400 resources)
    max_pairs_per_stack = MAX_RESOURCES_PER_STACK // 2

    if len(all_resources) <= max_pairs_per_stack:
        # Single template
        template = CFN_HEADER.format(description=f"KMS stack for perf test {env_id}")
        template += "Resources:\n"
        for _, _, content in all_resources:
            template += content
        templates.append(("kms.yaml", template))
    else:
        # Split into multiple templates
        for chunk_idx in range((len(all_resources) + max_pairs_per_stack - 1) // max_pairs_per_stack):
            start = chunk_idx * max_pairs_per_stack
            end = min(start + max_pairs_per_stack, len(all_resources))
            chunk = all_resources[start:end]

            template = CFN_HEADER.format(description=f"KMS stack {chunk_idx} for perf test {env_id}")
            template += "Resources:\n"
            for _, _, content in chunk:
                template += content
            templates.append((f"kms-{chunk_idx}.yaml", template))

    return templates


def generate_cfn_application(counts: ResourceCounts, env_id: str, iam_stack: str) -> str:
    """Generate CloudFormation template for application resources (legacy single template)."""
    templates = generate_cfn_application_split(counts, env_id, iam_stack)
    if len(templates) == 1:
        return templates[0][1]
    return templates[0][1]


def generate_cfn_application_split(counts: ResourceCounts, env_id: str, iam_stack_base: str) -> List[Tuple[str, str]]:
    """Generate CloudFormation templates for application resources, splitting if needed.

    Lambda functions reference IAM roles from split IAM stacks. The iam_stack_base is the base name
    (e.g., "perf-xxx-iam"), and we append the stack index if IAM is split.
    """
    templates = []
    all_resources = []

    # Calculate lambda_roles_count same as PKL
    min_other_roles = 1 if (counts.iam_policies > 0 or counts.instance_profiles > 0) else 0
    lambda_roles_count = min(
        max(5, int(counts.iam_roles * 0.2)),
        counts.iam_roles - min_other_roles
    )

    # Determine if IAM is split
    total_iam_resources = counts.iam_roles + counts.iam_policies + counts.instance_profiles
    iam_is_split = total_iam_resources > MAX_RESOURCES_PER_STACK

    # Lambda functions
    for i in range(counts.lambdas):
        role_idx = i % lambda_roles_count
        # Calculate which IAM stack the role is in
        if iam_is_split:
            iam_stack_idx = role_idx // MAX_RESOURCES_PER_STACK
            iam_stack = f"{iam_stack_base}-{iam_stack_idx}"
        else:
            iam_stack = iam_stack_base

        content = f"""
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
        all_resources.append(('lambda', i, content))

    # ECR Repositories
    for i in range(counts.ecr_repos):
        content = f"""
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
        all_resources.append(('ecr_repo', i, content))

    # Split into stacks
    if len(all_resources) <= MAX_RESOURCES_PER_STACK:
        template = CFN_HEADER.format(description=f"Application stack for perf test {env_id}")
        template += "Resources:\n"
        for _, _, content in all_resources:
            template += content
        templates.append(("application.yaml", template))
    else:
        chunks = split_into_chunks(all_resources, MAX_RESOURCES_PER_STACK)
        for chunk_idx, chunk_resources in chunks:
            template = CFN_HEADER.format(description=f"Application stack {chunk_idx} for perf test {env_id}")
            template += "Resources:\n"
            for _, _, content in chunk_resources:
                template += content
            templates.append((f"application-{chunk_idx}.yaml", template))

    return templates


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
    """Generate deployment shell script with S3 support for large templates."""
    deploy_commands = []
    for stack in stacks:
        deploy_commands.append(f"""
echo "Deploying {stack['name']}..."
aws cloudformation deploy \\
    --stack-name {env_id}-{stack['name']} \\
    --template-file {stack['file']} \\
    --capabilities CAPABILITY_NAMED_IAM \\
    --region {region} \\
    $S3_BUCKET_ARG \\
    --no-fail-on-empty-changeset
""")

    return f"""#!/bin/bash
# Deploy script for {env_id}
# Generated by generate-perf-test-env.py

set -e

REGION="{region}"
ENV_ID="{env_id}"

# S3 bucket for large templates (>51KB) - set this if deployment fails with size error
# Create bucket: aws s3 mb s3://your-bucket-name --region $REGION
S3_BUCKET="${{S3_BUCKET:-}}"
if [ -n "$S3_BUCKET" ]; then
    S3_BUCKET_ARG="--s3-bucket $S3_BUCKET"
    echo "Using S3 bucket: $S3_BUCKET for large templates"
else
    S3_BUCKET_ARG=""
    echo "No S3 bucket set. If templates exceed 51KB, set S3_BUCKET env var."
fi

echo "Deploying CloudFormation stacks ({len(stacks)} stacks) for $ENV_ID in $REGION"
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
        description="Generate a large-scale multi-cloud infrastructure environment for performance testing formae.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Generate PKL files for formae (AWS only - default)
  %(prog)s --count 100 --region us-east-1
  %(prog)s --count 1000 --region eu-west-1 --output ./perf-test

  # Generate multi-cloud PKL files
  %(prog)s --count 50 --clouds aws,azure,gcp --region us-east-2 \\
      --subscription-azure <sub-id> --project-gcp <project-id>

  # Use scale profiles
  %(prog)s --scale-profile medium --clouds aws,azure,gcp \\
      --subscription-azure <sub-id> --project-gcp <project-id>

  # Generate CloudFormation templates for discovery testing (AWS only)
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
        help="Target number of resources to generate per cloud (default: 100)"
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

    parser.add_argument(
        "--profile", "-p",
        type=str,
        choices=["default", "quota-safe"],
        default="default",
        help="AWS resource profile: 'default' for full resources, 'quota-safe' to avoid resources with tight quotas (EC2, EIP, VPC, etc.)"
    )

    # Multi-cloud arguments
    parser.add_argument(
        "--clouds",
        type=str,
        default="aws",
        help="Comma-separated list of clouds: aws, azure, gcp (default: aws)"
    )

    parser.add_argument(
        "--scale-profile",
        type=str,
        choices=["small", "medium", "large", "xl"],
        default=None,
        help="Scale profile overrides --count with per-cloud splits: small=500, medium=5000, large=20000, xl=50000"
    )

    parser.add_argument(
        "--subscription-azure",
        type=str,
        default=None,
        help="Azure subscription ID (required when azure is in --clouds)"
    )

    parser.add_argument(
        "--project-gcp",
        type=str,
        default=None,
        help="GCP project ID (required when gcp is in --clouds)"
    )

    parser.add_argument(
        "--region-gcp",
        type=str,
        default="us-central1",
        help="GCP region (default: us-central1)"
    )

    parser.add_argument(
        "--run-id",
        type=str,
        default=None,
        help="Unique run ID for tagging (default: auto-generated UUID)"
    )

    args = parser.parse_args()

    # Parse clouds
    clouds = [c.strip().lower() for c in args.clouds.split(",")]
    for c in clouds:
        if c not in ("aws", "azure", "gcp"):
            print(f"Error: unknown cloud '{c}'. Must be aws, azure, or gcp.", file=sys.stderr)
            sys.exit(1)

    # Validate required arguments for each cloud
    if "azure" in clouds and not args.subscription_azure:
        print("Error: --subscription-azure is required when azure is in --clouds", file=sys.stderr)
        sys.exit(1)
    if "gcp" in clouds and not args.project_gcp:
        print("Error: --project-gcp is required when gcp is in --clouds", file=sys.stderr)
        sys.exit(1)

    # CloudFormation is AWS-only
    output_format = args.format if args.format != "cfn" else "cloudformation"
    if output_format == "cloudformation" and (len(clouds) > 1 or clouds[0] != "aws"):
        print("Error: CloudFormation format is only supported for AWS-only generation", file=sys.stderr)
        sys.exit(1)

    # Determine resource counts
    is_multicloud = len(clouds) > 1 or clouds != ["aws"]
    run_id = args.run_id or uuid.uuid4().hex[:12]

    if args.scale_profile:
        aws_count, azure_count, gcp_count = SCALE_PROFILES[args.scale_profile]
    else:
        aws_count = args.count
        azure_count = args.count
        gcp_count = args.count

    # Generate unique environment ID
    env_id = f"perf-{uuid.uuid4().hex[:8]}"
    stack_name = f"perf-test-{env_id}"

    # Calculate resource counts for each cloud
    aws_counts = None
    azure_counts = None
    gcp_counts = None

    if "aws" in clouds:
        if args.profile == "quota-safe":
            aws_counts = calculate_counts_quota_safe(aws_count)
        else:
            aws_counts = calculate_counts(aws_count)

    if "azure" in clouds:
        azure_counts = calculate_azure_counts(azure_count)

    if "gcp" in clouds:
        gcp_counts = calculate_gcp_counts(gcp_count)

    if args.dry_run:
        print(f"Clouds: {', '.join(c.upper() for c in clouds)}")
        if args.scale_profile:
            print(f"Scale profile: {args.scale_profile}")
        print(f"Run ID: {run_id}")

        if aws_counts:
            print(f"\n{'=' * 40}")
            print(f"AWS (target: {aws_count}, calculated: {aws_counts.total})")
            print(f"{'=' * 40}")
            print(f"  Profile: {args.profile}")
            print(f"\n  Networking (capped to stay within quotas):")
            print(f"    VPCs: {aws_counts.vpcs} (hard cap at 5)")
            print(f"    Subnets: {aws_counts.vpcs * aws_counts.subnets_per_vpc} ({aws_counts.subnets_per_vpc} per VPC)")
            print(f"    Route Tables: {aws_counts.route_tables}")
            print(f"    Routes: {aws_counts.routes}")
            print(f"    Internet Gateways: {aws_counts.igws} (1 per VPC)")
            print(f"    NAT Gateways: {aws_counts.nat_gws} (capped at 5)")
            print(f"    Elastic IPs: {aws_counts.eips}")
            print(f"    Security Groups: {aws_counts.security_groups}")
            print(f"    SG Ingress Rules: {aws_counts.sg_ingress_rules}")
            print(f"    SG Egress Rules: {aws_counts.sg_egress_rules}")
            print(f"    VPC Endpoints: {aws_counts.vpc_endpoints}")
            print(f"\n  Compute:")
            print(f"    EC2 Instances: {aws_counts.ec2_instances}")
            print(f"    EBS Volumes: {aws_counts.ebs_volumes}")
            print(f"    Launch Templates: {aws_counts.launch_templates}")
            print(f"\n  IAM & Security:")
            print(f"    IAM Roles: {aws_counts.iam_roles}")
            print(f"    IAM Policies: {aws_counts.iam_policies}")
            print(f"    Instance Profiles: {aws_counts.instance_profiles}")
            print(f"    KMS Keys: {aws_counts.kms_keys}")
            print(f"    KMS Aliases: {aws_counts.kms_aliases}")
            print(f"    Secrets: {aws_counts.secrets}")
            print(f"\n  Storage:")
            print(f"    S3 Buckets: {aws_counts.s3_buckets}")
            print(f"    DynamoDB Tables: {aws_counts.dynamodb_tables}")
            print(f"    EFS File Systems: {aws_counts.efs_filesystems}")
            print(f"    EFS Mount Targets: {aws_counts.efs_mount_targets}")
            print(f"\n  Observability:")
            print(f"    CloudWatch Log Groups: {aws_counts.log_groups}")
            print(f"    SQS Queues: {aws_counts.sqs_queues}")
            print(f"\n  Application:")
            print(f"    Lambda Functions: {aws_counts.lambdas}")
            print(f"    ECR Repositories: {aws_counts.ecr_repos}")
            print(f"\n  DNS & API Gateway:")
            print(f"    Route53 Hosted Zones: {aws_counts.route53_zones}")
            print(f"    Route53 Records: {aws_counts.route53_records}")
            print(f"    API Gateways: {aws_counts.api_gateways}")
            print(f"    API Stages: {aws_counts.api_stages}")
            print(f"\n  RDS:")
            print(f"    RDS Instances: {aws_counts.rds_instances}")
            print(f"    DB Subnet Groups: {aws_counts.db_subnet_groups}")
            print(f"    DB Parameter Groups: {aws_counts.db_parameter_groups}")

        if azure_counts:
            print(f"\n{'=' * 40}")
            print(f"Azure (target: {azure_count}, calculated: {azure_counts.total})")
            print(f"{'=' * 40}")
            print(f"\n  Networking:")
            print(f"    Resource Groups: {azure_counts.resource_groups}")
            print(f"    Virtual Networks: {azure_counts.virtual_networks}")
            print(f"    Subnets: {azure_counts.virtual_networks * azure_counts.subnets_per_vnet}")
            print(f"    NSGs: {azure_counts.nsgs}")
            print(f"    Public IPs: {azure_counts.public_ips}")
            print(f"\n  Compute:")
            print(f"    Virtual Machines: {azure_counts.virtual_machines}")
            print(f"    Network Interfaces: {azure_counts.network_interfaces}")
            print(f"\n  Storage:")
            print(f"    Storage Accounts: {azure_counts.storage_accounts}")
            print(f"\n  IAM:")
            print(f"    Managed Identities: {azure_counts.managed_identities}")
            print(f"\n  Security:")
            print(f"    Key Vaults: {azure_counts.key_vaults}")
            print(f"    Container Registries: {azure_counts.container_registries}")
            print(f"\n  Database:")
            print(f"    PostgreSQL Flex Servers: {azure_counts.postgres_servers}")

        if gcp_counts:
            print(f"\n{'=' * 40}")
            print(f"GCP (target: {gcp_count}, calculated: {gcp_counts.total})")
            print(f"{'=' * 40}")
            print(f"\n  Networking:")
            print(f"    Networks: {gcp_counts.compute_networks}")
            print(f"    Subnetworks: {gcp_counts.compute_subnetworks}")
            print(f"    Firewalls: {gcp_counts.compute_firewalls}")
            print(f"    Addresses: {gcp_counts.compute_addresses}")
            print(f"\n  Compute:")
            print(f"    Instances: {gcp_counts.compute_instances}")
            print(f"    Disks: {gcp_counts.compute_disks}")
            print(f"\n  Storage:")
            print(f"    Buckets: {gcp_counts.storage_buckets}")
            print(f"\n  Database:")
            print(f"    SQL Instances: {gcp_counts.sql_instances}")
            print(f"    BigQuery Datasets: {gcp_counts.bigquery_datasets}")

        grand_total = sum(c.total for c in [aws_counts, azure_counts, gcp_counts] if c)
        print(f"\n{'=' * 40}")
        print(f"Grand Total: {grand_total} resources")
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

    if output_format == "pkl":
        if is_multicloud or len(clouds) > 1 or (len(clouds) == 1 and clouds[0] != "aws"):
            # Multi-cloud or single non-AWS cloud: use subdirectory layout
            _generate_multicloud_pkl(
                output_dir=output_dir,
                formae_path=formae_path,
                env_id=env_id,
                run_id=run_id,
                stack_name=stack_name,
                clouds=clouds,
                region=args.region,
                aws_counts=aws_counts,
                azure_counts=azure_counts,
                gcp_counts=gcp_counts,
                azure_subscription=args.subscription_azure,
                gcp_project=args.project_gcp,
                gcp_region=args.region_gcp,
            )
        else:
            # AWS-only: use the original flat layout for backwards compatibility
            _generate_aws_only_pkl(
                output_dir=output_dir,
                formae_path=formae_path,
                env_id=env_id,
                stack_name=stack_name,
                region=args.region,
                counts=aws_counts,
            )
    else:
        # CloudFormation (AWS only)
        _generate_cloudformation(
            output_dir=output_dir,
            env_id=env_id,
            stack_name=stack_name,
            region=args.region,
            counts=aws_counts,
            is_quota_safe=(args.profile == "quota-safe"),
        )


def _generate_aws_only_pkl(
    output_dir: str,
    formae_path: str,
    env_id: str,
    stack_name: str,
    region: str,
    counts: ResourceCounts,
):
    """Generate AWS-only PKL files (backwards-compatible flat layout)."""
    files = {
        "PklProject": generate_pkl_project(formae_path),
        "vars.pkl": generate_vars_pkl(env_id, region, stack_name),
        "networking.pkl": generate_networking_pkl(counts, region),
        "security_groups.pkl": generate_security_groups_pkl(counts),
        "compute.pkl": generate_compute_pkl(counts, region),
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
        "README.md": generate_readme(env_id, stack_name, region, counts),
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
    print(f"Region: {region}")
    print(f"Target Resources: {counts.total}")
    print(f"Output Directory: {output_dir}")
    print(f"\nNext steps:")
    print(f"  1. cd {output_dir}")
    print(f"  2. pkl project resolve")
    print(f"  3. formae apply --simulate main.pkl  # dry-run")
    print(f"  4. formae apply main.pkl             # deploy")
    print(f"  5. formae destroy --stack {stack_name}  # cleanup")


def _generate_multicloud_pkl(
    output_dir: str,
    formae_path: str,
    env_id: str,
    run_id: str,
    stack_name: str,
    clouds: list,
    region: str,
    aws_counts: ResourceCounts,
    azure_counts: AzureResourceCounts,
    gcp_counts: GCPResourceCounts,
    azure_subscription: str = None,
    gcp_project: str = None,
    gcp_region: str = "us-central1",
):
    """Generate multi-cloud PKL files with subdirectory layout."""

    # Azure location mapping from AWS region
    azure_location_map = {
        "us-east-1": "eastus",
        "us-east-2": "eastus2",
        "us-west-1": "westus",
        "us-west-2": "westus2",
        "eu-west-1": "westeurope",
        "eu-central-1": "germanywestcentral",
    }
    azure_location = azure_location_map.get(region, "eastus")

    # Generate PklProject
    files = {
        "PklProject": generate_multicloud_pkl_project(formae_path, clouds),
    }

    # Generate vars.pkl
    files["vars.pkl"] = generate_multicloud_vars_pkl(
        env_id=env_id,
        run_id=run_id,
        clouds=clouds,
        region=region,
        stack_name=stack_name,
        azure_subscription=azure_subscription,
        azure_location=azure_location,
        gcp_project=gcp_project,
        gcp_region=gcp_region,
    )

    # Generate main.pkl
    files["main.pkl"] = generate_multicloud_main_pkl(
        env_id=env_id,
        clouds=clouds,
        aws_counts=aws_counts,
        azure_counts=azure_counts,
        gcp_counts=gcp_counts,
    )

    # Each cloud subdirectory gets a symlink to the parent vars.pkl
    # This is simpler than trying to extend/import since the module files use `import "./vars.pkl"`

    # Generate AWS files
    if "aws" in clouds and aws_counts:
        aws_dir = os.path.join(output_dir, "aws")
        os.makedirs(aws_dir, exist_ok=True)
        # Symlink vars.pkl from parent so module imports work
        os.symlink("../vars.pkl", os.path.join(aws_dir, "vars.pkl"))
        aws_files = {
            "networking.pkl": generate_networking_pkl(aws_counts, region),
            "security_groups.pkl": generate_security_groups_pkl(aws_counts),
            "compute.pkl": generate_compute_pkl(aws_counts, region),
            "iam.pkl": generate_iam_pkl(aws_counts),
            "kms.pkl": generate_kms_pkl(aws_counts),
            "storage.pkl": generate_storage_pkl(aws_counts),
            "observability.pkl": generate_observability_pkl(aws_counts),
            "secrets.pkl": generate_secrets_pkl(aws_counts),
            "application.pkl": generate_application_pkl(aws_counts),
            "route53.pkl": generate_route53_pkl(aws_counts),
            "api_gateway.pkl": generate_api_gateway_pkl(aws_counts),
            "rds.pkl": generate_rds_pkl(aws_counts),
            "main.pkl": generate_main_pkl(env_id, f"{stack_name}-aws", aws_counts,
                                          stack_var="awsStack", target_var="awsTarget",
                                          region_var="awsRegion"),
        }
        for filename, content in aws_files.items():
            filepath = os.path.join(aws_dir, filename)
            with open(filepath, "w") as f:
                f.write(content)
            print(f"Generated: {filepath}")

    # Generate Azure files
    if "azure" in clouds and azure_counts:
        az_dir = os.path.join(output_dir, "azure")
        os.makedirs(az_dir, exist_ok=True)
        os.symlink("../vars.pkl", os.path.join(az_dir, "vars.pkl"))
        azure_files = {
            "networking.pkl": generate_azure_networking_pkl(azure_counts, azure_location),
            "storage.pkl": generate_azure_storage_pkl(azure_counts, azure_location),
            "iam.pkl": generate_azure_iam_pkl(azure_counts, azure_location),
            "security.pkl": generate_azure_security_pkl(azure_counts, azure_location),
            "main.pkl": generate_azure_main_pkl(env_id, azure_counts),
        }
        if azure_counts.virtual_machines > 0 or azure_counts.network_interfaces > 0:
            azure_files["compute.pkl"] = generate_azure_compute_pkl(azure_counts, azure_location)
        if azure_counts.postgres_servers > 0:
            azure_files["database.pkl"] = generate_azure_database_pkl(azure_counts, azure_location)
        for filename, content in azure_files.items():
            filepath = os.path.join(az_dir, filename)
            with open(filepath, "w") as f:
                f.write(content)
            print(f"Generated: {filepath}")

    # Generate GCP files
    if "gcp" in clouds and gcp_counts:
        gcp_dir = os.path.join(output_dir, "gcp")
        os.makedirs(gcp_dir, exist_ok=True)
        os.symlink("../vars.pkl", os.path.join(gcp_dir, "vars.pkl"))
        gcp_files = {
            "networking.pkl": generate_gcp_networking_pkl(gcp_counts, gcp_region),
            "storage.pkl": generate_gcp_storage_pkl(gcp_counts, gcp_region),
            "main.pkl": generate_gcp_main_pkl(env_id, gcp_counts),
        }
        if gcp_counts.compute_instances > 0 or gcp_counts.compute_disks > 0:
            gcp_files["compute.pkl"] = generate_gcp_compute_pkl(gcp_counts, gcp_region, gcp_project)
        if gcp_counts.sql_instances > 0 or gcp_counts.bigquery_datasets > 0:
            gcp_files["database.pkl"] = generate_gcp_database_pkl(gcp_counts, gcp_region, gcp_project)
        for filename, content in gcp_files.items():
            filepath = os.path.join(gcp_dir, filename)
            with open(filepath, "w") as f:
                f.write(content)
            print(f"Generated: {filepath}")

    # Write top-level files
    for filename, content in files.items():
        filepath = os.path.join(output_dir, filename)
        with open(filepath, "w") as f:
            f.write(content)
        print(f"Generated: {filepath}")

    grand_total = sum(c.total for c in [aws_counts, azure_counts, gcp_counts] if c)
    print(f"\n{'=' * 60}")
    print(f"Multi-cloud stress test environment generated successfully!")
    print(f"{'=' * 60}")
    print(f"\nFormat: PKL (for formae)")
    print(f"Environment ID: {env_id}")
    print(f"Run ID: {run_id}")
    print(f"Clouds: {', '.join(c.upper() for c in clouds)}")
    if aws_counts:
        print(f"  AWS: ~{aws_counts.total} resources (region: {region})")
    if azure_counts:
        print(f"  Azure: ~{azure_counts.total} resources (location: {azure_location})")
    if gcp_counts:
        print(f"  GCP: ~{gcp_counts.total} resources (region: {gcp_region})")
    print(f"Grand Total: ~{grand_total} resources")
    print(f"Output Directory: {output_dir}")
    print(f"\nNext steps:")
    print(f"  1. cd {output_dir}")
    print(f"  2. pkl project resolve")
    print(f"  3. formae apply --simulate main.pkl  # dry-run")
    print(f"  4. formae apply main.pkl             # deploy")


def _generate_cloudformation(
    output_dir: str,
    env_id: str,
    stack_name: str,
    region: str,
    counts: ResourceCounts,
    is_quota_safe: bool,
):
    """Generate CloudFormation templates (AWS only)."""
    networking_stack = f"{env_id}-networking"
    sg_stack_base = f"{env_id}-security-groups"
    iam_stack_base = f"{env_id}-iam"

    # Collect all templates and track stacks for deploy/destroy scripts
    files = {}
    stacks = []

    # Networking (single stack - skip in quota-safe mode)
    if not is_quota_safe:
        files["networking.yaml"] = generate_cfn_networking(counts, env_id, region)
        stacks.append({"name": "networking", "file": "networking.yaml"})

    # Security groups (may be split - skip in quota-safe mode)
    sg_stack = None
    if not is_quota_safe:
        sg_templates = generate_cfn_security_groups_split(counts, env_id, networking_stack)
        for filename, content in sg_templates:
            files[filename] = content
            sn = filename.replace(".yaml", "")
            stacks.append({"name": sn, "file": filename})
        sg_stack = sg_stack_base if len(sg_templates) == 1 else f"{sg_stack_base}-0"

    # IAM (may be split)
    iam_templates = generate_cfn_iam_split(counts, env_id)
    for filename, content in iam_templates:
        files[filename] = content
        sn = filename.replace(".yaml", "")
        stacks.append({"name": sn, "file": filename})

    # KMS (may be split if many keys)
    kms_templates = generate_cfn_kms_split(counts, env_id)
    for filename, content in kms_templates:
        files[filename] = content
        sn = filename.replace(".yaml", "")
        stacks.append({"name": sn, "file": filename})

    # Storage (may be split)
    storage_templates = generate_cfn_storage_split(counts, env_id, networking_stack, sg_stack or "")
    for filename, content in storage_templates:
        files[filename] = content
        sn = filename.replace(".yaml", "")
        stacks.append({"name": sn, "file": filename})

    # Compute (may be split - skip in quota-safe mode)
    if not is_quota_safe:
        compute_templates = generate_cfn_compute_split(counts, env_id, region, networking_stack, sg_stack_base)
        for filename, content in compute_templates:
            files[filename] = content
            sn = filename.replace(".yaml", "")
            stacks.append({"name": sn, "file": filename})

    # Observability (may be split)
    obs_templates = generate_cfn_observability_split(counts, env_id)
    for filename, content in obs_templates:
        files[filename] = content
        sn = filename.replace(".yaml", "")
        stacks.append({"name": sn, "file": filename})

    # Secrets (single stack - usually small)
    files["secrets.yaml"] = generate_cfn_secrets(counts, env_id)
    stacks.append({"name": "secrets", "file": "secrets.yaml"})

    # Application (may be split)
    app_templates = generate_cfn_application_split(counts, env_id, iam_stack_base)
    for filename, content in app_templates:
        files[filename] = content
        sn = filename.replace(".yaml", "")
        stacks.append({"name": sn, "file": filename})

    # Route53 (single stack - usually small)
    files["route53.yaml"] = generate_cfn_route53(counts, env_id)
    stacks.append({"name": "route53", "file": "route53.yaml"})

    # API Gateway (single stack)
    files["api-gateway.yaml"] = generate_cfn_api_gateway(counts, env_id)
    stacks.append({"name": "api-gateway", "file": "api-gateway.yaml"})

    # RDS (single stack - skip in quota-safe mode, requires VPC)
    if not is_quota_safe:
        files["rds.yaml"] = generate_cfn_rds(counts, env_id, networking_stack, sg_stack)
        stacks.append({"name": "rds", "file": "rds.yaml"})

    # Generate deploy and destroy scripts
    files["deploy.sh"] = generate_cfn_deploy_script(env_id, region, stacks)
    files["destroy.sh"] = generate_cfn_destroy_script(env_id, region, stacks)
    files["README.md"] = generate_cfn_readme(env_id, region, counts)

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
    print(f"Region: {region}")
    print(f"Calculated Resources: {counts.total}")
    print(f"Output Directory: {output_dir}")
    print(f"CloudFormation Stacks: {len(stacks)}")
    print(f"\nNext steps:")
    print(f"  1. cd {output_dir}")
    print(f"  2. ./deploy.sh             # deploy all stacks")
    print(f"  3. # Test formae discovery:")
    print(f"     formae agent start")
    print(f"     formae discover --target aws --region {region}")
    print(f"     formae inventory --unmanaged")
    print(f"  4. ./destroy.sh            # cleanup")


if __name__ == "__main__":
    main()
