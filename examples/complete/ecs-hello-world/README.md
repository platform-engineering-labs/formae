# ecs-hello-world Example
This example demonstrates how to provision a simple ECS (Elastic Container Service) application stack. The only included forma `ecs_hello_world.pkl` is supposed to be used holistically, in the replace mode when applied, and and also to be holistically destroyed. The forma has deliberately not been
broken further down in order to demonstrate that modularization is optional, and any granularity is accepted by **formae**.

## What Gets Provisioned
1. Network: Provisions the foundational networking resources, such as a VPC, subnets, and routing tables, required for the ECS cluster and its services.

2. Security Groups: Adds security groups to control inbound and outbound traffic for the ECS cluster, load balancer, and services.

3. ECS Cluster: Creates an ECS cluster that will host your containerized services.

4. Load Balancer: Provisions an Application Load Balancer (ALB) to distribute traffic to your ECS services.

5. Service: Deploys an ECS service (such as a containerized web application) behind the load balancer. Please keep in mind that deploying custom workloads is not **formae**'s responsibility, thus the ECS service is deployed as an example.

## Provisioning the resources
Ensure the **formae** node is up and running, then run the following command in order

`formae apply --watch /opt/pel/formae/examples/complete/ecs-hello-world/ecs_hello_world.pkl`

Once the asynchronous command has successfully finished, you should have a fully functional ECS stack with networking, security, cluster, load balancer, and a running service.

## Destroying the resources

Ensure the **formae** node is up and running, then run the following command:

`formae destroy --watch --forma-file /opt/pel/formae/examples/complete/ecs-hello-world/ecs_hello_world.pkl`
