# © 2025 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

import json
import os
from datetime import datetime, timezone

def lambda_handler(event, context):
    db_host = os.environ.get('DB_HOST', 'N/A')
    db_port = os.environ.get('DB_PORT', 'N/A')
    data_bucket = os.environ.get('DATA_BUCKET', 'N/A')
    deployment_bucket = os.environ.get('DEPLOYMENT_BUCKET', 'N/A')
    
    return {
        'statusCode': 200,
        'headers': {'Content-Type': 'application/json'},
        'body': json.dumps({
            'message': 'Hello World from Lambda with Environment Variables!',
            'timestamp': datetime.now(timezone.utc).isoformat(),
            'function_info': {
                'name': context.function_name,
                'memory_limit': context.memory_limit_in_mb,
                'request_id': context.aws_request_id
            },
            'environment_variables': {
                'database': {
                    'host': db_host,
                    'port': db_port,
                },
                'buckets': {
                    'data_bucket': data_bucket,
                    'deployment_bucket': deployment_bucket
                }
            },
            'vpc_info': {
                'region': os.environ.get('AWS_REGION')
            }
        })
    }

# Local Test Code
if __name__ == '__main__':
    os.environ['DB_HOST'] = 'localhost'
    os.environ['DB_PORT'] = '5432'
    os.environ['DATA_BUCKET'] = 'test-data-bucket'
    os.environ['DEPLOYMENT_BUCKET'] = 'test-deployment-bucket'
    os.environ['AWS_REGION'] = 'us-east-2'
    
    class MockContext:
        def __init__(self):
            self.function_name = "my-local-hello-world-function"
            self.memory_limit_in_mb = 256
            self.aws_request_id = "test-request-id-12345"
            self.invoked_function_arn = "arn:aws:lambda:us-east-2:123456789012:function:test-function"

    response = lambda_handler({}, MockContext())
    response_body_parsed = json.loads(response['body'])
    pretty_response = {
        'statusCode': response['statusCode'],
        'headers': response['headers'],
        'body': response_body_parsed
    }

    print(json.dumps(pretty_response, indent=2))