# Static Website Infrastructure with PKL

This example demonstrates how to provision AWS infrastructure for a static website.

## Files

- `/opt/pel/formae/examples/complete/static-website/main.pkl` - Main infra entry point
- `/opt/pel/formae/examples/complete/static-website/vars.pkl` - Config variables
- `/opt/pel/formae/examples/complete/static-website/components/s3_website.pkl` - S3 bucket and policy config
- `/opt/pel/formae/examples/complete/static-website/components/cloudfront.pkl` - CloudFront distribution
- `/opt/pel/formae/examples/complete/static-website/components/dns.pkl` - Route53 config
- `/opt/pel/formae/examples/complete/static-website/site/` - Website content

## Usage

1. Configure variables in `/opt/pel/formae/examples/complete/static-website/vars.pkl`
2. Deploy to AWS:

   Ensure the **formae** node is up and running. Then run:

   `formae apply --watch /opt/pel/formae/examples/complete/static-website/main.pkl`

3. Upload website content:

   **formae** is not (necessarily) taking care of deployments of custom workloads, content and the likes. So
   in this case, you need to opt out to manually deploying the test site we provide:

   `aws s3 cp index.html s3://<ProjectName><BucketSuffix> --region <Region>`

## Test Your Site

After the above steps are done, your site should be available at:

- S3 website URL: http://<ProjectName><BucketSuffix>.s3-website.<Region>.amazonaws.com
- CloudFront URL - you need to run the extract command with **formae** and parse / capture the output in order to
receive the resulting, automatically generated domain name:

`formae extract static-website-stack | grep DomainName`

or extract it in the way you prefer. The domain should be accessible publicly in your browser.

## Known bugs

Unfortunately, at the moment, destroying the resources in this example isn't possible due to a bug
that will be fixed in one of the next deliveries.
