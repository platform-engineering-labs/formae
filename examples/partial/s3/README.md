# s3 Example

This example demonstrates how to provision a simple, secure Amazon S3 bucket.

## What Gets Provisioned

- **S3 Bucket**: A uniquely named S3 bucket with versioning enabled.
- **Tags**: The bucket is tagged with `Name`, `Project`, and `Environment` for easy identification and management.
- **Public Access Block**: All public access to the bucket is blocked for security.
- **Encryption**: The bucket uses server-side encryption with AES256.

## Usage

To provision the S3 bucket, run:

`formae apply /opt/pel/formae/examples/partial/s3/s3_example.pkl`

To destroy the S3 bucket, run:

`formae destroy --forma-file /opt/pel/formae/examples/partial/s3/s3_example.pkl`