# Lambda Environment Variables Infrastructure with Pkl

This example demonstrates how to provision and deploy an AWS Lambda function with environment variables that reference other AWS resources using Forma and Pkl.

## Files

- `/opt/pel/formae/examples/complete/lambda-env/apply_infra.pkl` - Infrastructure provisioning entry point
- `/opt/pel/formae/examples/complete/lambda-env/patch_lambda.pkl` - Lambda function deployment
- `/opt/pel/formae/examples/complete/lambda-env/vars.pkl` - Configuration variables
- `/opt/pel/formae/examples/complete/lambda-env/infrastructure/network.pkl` - VPC and networking components
- `/opt/pel/formae/examples/complete/lambda-env/infrastructure/security.pkl` - IAM roles and security groups
- `/opt/pel/formae/examples/complete/lambda-env/infrastructure/storage.pkl` - S3 buckets and RDS database
- `/opt/pel/formae/examples/complete/lambda-env/code/hello.py` - Lambda function source code
- `/opt/pel/formae/examples/complete/lambda-env/s3objects/hello.zip` - Lambda deployment package

## Usage

1. Configure variables in `/opt/pel/formae/examples/complete/lambda-env/vars.pkl`

2. Apply Infrastructure:

    Ensure the **formae** node is up and running. Then run:

    ```bash
    formae apply --watch /opt/pel/formae/examples/complete/lambda-env/apply_infra.pkl
    ```

3. Upload Lambda Packages:

    Change to the s3objects directory and upload the Lambda deployment packages to the S3 bucket:

    ```bash
    cd /opt/pel/formae/examples/complete/lambda-env/s3objects
    aws s3 cp hello.zip s3://<ProjectName>-deployment-<Region>/
    ```

    > **Note**: Replace `<ProjectName>` and `<Region>` with your actual values from vars.pkl

4. Deploy Lambda Function:

    Use Patch mode to deploy the Lambda function with environment variables:

    ```bash
    formae apply --mode patch --watch /opt/pel/formae/examples/complete/lambda-env/patch_lambda.pkl
    ```

## Testing Your Lambda Function

After deployment is complete, you can test the deployed Lambda function:

1. Test the hello function to see environment variables:

    ```bash
    aws lambda invoke --function-name <ProjectName>-hello response-hello.json
    ```

2. View the response:

    ```bash
    cat response-hello.json | jq '.body | fromjson'
    ```

3. Check function logs:

    ```bash
    aws logs describe-log-groups --log-group-name-prefix "/aws/lambda/<ProjectName>-hello"
    ```

4. View recent log events:

    ```bash
    aws logs tail "/aws/lambda/<ProjectName>-hello" --follow
    ```

## Troubleshooting

- If Lambda function fails to deploy, check that S3 bucket exists and hello.zip was uploaded- If function has VPC timeout issues, verify security groups allow outbound HTTPS traffic
- Check CloudWatch logs for detailed error messages
- Ensure IAM role has proper permissions for VPC Lambda execution

## Cleanup

Remove S3 bucket contents before destroying:

```bash
aws s3 rm s3://<ProjectName>-deployment-<Region>/ --recursive
```

Then destroy the infrastructure:

```bash
formae destroy --watch --forma-file /opt/pel/formae/examples/complete/lambda-env/patch_lambda.pkl
formae destroy --watch --forma-file /opt/pel/formae/examples/complete/lambda-env/apply_infra.pkl
```

## Known Issues on Destroy

- RDS DBInstance deletion can be slow, sometimes causing DBSubnetGroup deletion to fail, requiring manual intervention. This will be resolved in a future release.
