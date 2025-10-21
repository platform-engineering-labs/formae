# Machine Learning Platform Infrastructure with Pkl

This example demonstrates how to provision an ML platform built on Amazon SageMaker with supporting infrastructure.

## Files

- `/opt/pel/formae/examples/complete/ml-platform/main.pkl` - Main infrastructure entry point
- `/opt/pel/formae/examples/complete/ml-platform/infrastructure/platform.pkl` - ML platform orchestration and component integration
- `/opt/pel/formae/examples/complete/ml-platform/infrastructure/networking.pkl` - VPC and network components
- `/opt/pel/formae/examples/complete/ml-platform/infrastructure/security.pkl` - IAM roles and policies
- `/opt/pel/formae/examples/complete/ml-platform/infrastructure/storage.pkl` - S3 buckets, database, EFS, and KMS keys
- `/opt/pel/formae/examples/complete/ml-platform/infrastructure/containers.pkl` - ECR repositories and ECS cluster
- `/opt/pel/formae/examples/complete/ml-platform/infrastructure/sagemaker.pkl` - SageMaker domain and user profiles
- `/opt/pel/formae/examples/complete/ml-platform/infrastructure/monitoring.pkl` - CloudWatch logs and monitoring
- `/opt/pel/formae/examples/complete/ml-platform/vars.pkl` - Configuration variables and parameters

## Usage

1. Configure variables in `vars.pkl`

2. Deploy to AWS: 
    
    Ensure the **formae** node is up and running. Then run:

    ```bash
    formae apply --watch /opt/pel/formae/examples/complete/ml-platform/main.pkl
    ```

## Accessing Your ML Platform

After deployment is complete, you can access your SageMaker domain and other resources:

1. Verify SageMaker is Accessible:

    ```bash
    # Get the SageMaker domain ID
    aws sagemaker list-domains --region <Region> --query "Domains[?DomainName=='ml-platform-domain'].DomainId" --output table

    # Get the SageMaker domain URL using the ID obtained above
    aws sagemaker describe-domain --domain-id <DomainId> --region <Region> --query 'Url'
    ```

2. Check S3 buckets for data and models:
    ```bash
    # Look at buckets
    aws s3 ls

    # Examine contents
    aws s3 ls s3://<ProjectName>-training-data-bucket
    aws s3 ls s3://<ProjectName>-model-artifacts-bucket
    ```

3. Verify ECR repositories:
    ```bash
    aws ecr describe-repositories --repository-names <ProjectName>/ml-workflow <ProjectName>/feature-engineering --region <Region>
    ```

4. Access Postgres DB:
    ```bash
    # Get the DB Identifier
    aws rds describe-db-instances --region <Region> --query "DBInstances[].DBInstanceIdentifier" --output text

    # Get the database endpoint using the identifier above
    aws rds describe-db-instances --db-instance-identifier <YourDBInstanceId> --region <Region> --query "DBInstances[0].Endpoint.Address" --output text    
    ```

## Known Issues
These issues are actively being addressed and will be resolved in a future release.

1. SageMaker automatically creates its own EFS file system (HomeEFS) and associated resources, even when configured to use a custom EFS.
    - This can block VPC deletion and may require manual cleanup
    - HomeEFS is not mounted due to `AutoMountHomeEFS = "Disabled"`, yes still exists and must be manually deleted
    - See [issue #1085](https://github.com/aws-cloudformation/cloudformation-coverage-roadmap/issues/1085)

2. EC2 RouteTable may be recreated unnecessarily on each apply and may not be deleted on destroy due to dependency resolution issues.
