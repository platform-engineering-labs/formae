# Secret-Resolvable Example

This example demonstrates how to securely create and use an AWS Secrets Manager secret for a database password, and reference it directly in an RDS instance definition using **formae**.

## Files

- `/opt/pel/formae/examples/complete/secret-resolvable/main.pkl` – Main infra entry point (all resources defined inline for clarity)

## What this example shows

- How to define a secret in AWS Secrets Manager with a randomly generated password.
- How to reference the secret’s value in an RDS database resource.
- How to use `.opaque` to ensure the password is stored securely.
- How `.setOnce` ensures the secret value is only set on initial creation.
- Basic VPC, subnet, and security group setup for the database.

## Usage

1. Ensure the **formae** node is up and running.
2. Deploy to AWS:

   ```
   formae apply --watch /opt/pel/formae/examples/complete/secret-resolvable/main.pkl
   ```

## Cleanup

To destroy all resources created by this example, run:

```
formae destroy --watch /opt/pel/formae/examples/complete/secret-resolvable/main.pkl
```

---

For more advanced patterns and modularization, see the other examples in this repository.