# SQS Examples

This directory contains two examples for provisioning Amazon SQS (Simple Queue Service) resources. You can choose between a simple queue stack or a more advanced stack with multiple queue types and policies.

---

## 1. Simple Queue Stack

Provisions a single standard SQS queue with basic configuration and tags.

**What Gets Provisioned:**
- **Standard SQS Queue:**
  - Name: `simple-queue`
  - Visibility timeout: 300 seconds
  - Message retention: 4 days
  - Tagged with `Environment=Production`

## Usage:

To provision the simple queue, run:

`formae apply /opt/pel/formae/examples/partial/sqs/simple_queue.pkl``

To destroy the corresonding resources, run:

`formae destroy --forma-file /opt/pel/formae/examples/partial/sqs/simple_queue.pkl`

## 2. Advanced SQS Stack
Provisions multiple SQS queues (standard, FIFO, dead-letter), queue policies, and inline policies.

**What Gets Provisioned:**

- Standard Queue:
  - Configurable name (default: `pel-sqs-test-queue`)
  - Tags: `Environment=Production`
- FIFO Queue:
  - Name ends with `.fifo`
  - Content-based deduplication and throughput limits
  - Tags: `Type=FIFO`
- Dead Letter Queue (DLQ):
  - Used for failed message redrive
  - Tags: `Purpose=DeadLetter`
- Queue with DLQ:
  - Redrive policy to DLQ after 3 failed receives
- Queue Policy:
  - Allows specific IAM principals to send/receive messages
- Queue with Inline Policy:
  - Allows SNS topics to send messages

### Usage:

To provision the advanced example, run:

`formae apply /opt/pel/formae/examples/partial/sqs/sqs_queue.pkl`

To destroy the resources from the advanced example, run:

`formae destroy --forma-file /opt/pel/formae/examples/partial/sqs/sqs_queue.pkl`
