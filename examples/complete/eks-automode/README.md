# EKS Cluster Infrastructure with Pkl

This example demonstrates how to provision an AWS EKS Kubernetes cluster in Auto Mode. 

## Files

- `/opt/pel/formae/examples/complete/eks-automode/main.pkl` - Main infrastructure entry point
- `/opt/pel/formae/examples/complete/eks-automode/vars.pkl` - Configuration variables
- `/opt/pel/formae/examples/complete/eeks-automodeks/infrastructure/nvpc.pkl` - VPC configuration
- `/opt/pel/formae/examples/complete/eks-automode/infrastructure/network.pkl` - Network components (subnets, routes, etc.)
- `/opt/pel/formae/examples/complete/eks-automode/infrastructure/security_groups.pkl` - Security group definitions
- `/opt/pel/formae/examples/complete/eks-automode/infrastructure/iam.pkl` - IAM roles for EKS
- `/opt/pel/formae/examples/complete/eks-automode/infrastructure/eks_cluster.pkl` - EKS cluster configuration

## Usage

1. Configure variables in `/opt/pel/formae/examples/complete/eks/vars.pkl`

2. Deploy to AWS: 
    
    Ensure the **formae** node is up and running. Then run:

    ```bash
    formae apply --watch /opt/pel/formae/examples/complete/eks-automode/main.pkl
    ```

## Accessing Your Cluster

After Deployment is complete, you can configure `kubectl` to interact with your new EKS cluster:

1. Update your kubeconfig:

    ```bash
    aws eks update-kubeconfig --name <ProjectName> --region <Region>
    ```

2. Verify the connection:
    ```bash
    kubectl cluster-info
    ```

3. Check that cluster is Ready:
    ```bash
    aws eks describe-cluster --name <ProjectName> --region <Region> --query 'cluster.status'
    ```

4. Check Auto Mode resources:
    ```bash
    kubectl get nodepools
    kubectl get nodeclasses
    ```

5. Initial node check (Expected: No nodes shown):
    ```bash
    kubectl get nodes
    ```
    
    > **Note**: With Auto Mode enabled, you won't see any nodes initially. This is expected behavior - Auto Mode only provisions nodes when workloads require them

6. Test cluster by deploying a workload:
    ```bash
    # Deploy test application
    kubectl create deployment nginx-test --image=nginx --replicas=2
    
    # Watch nodes get created automatically (1-2 minutes)
    kubectl get nodes -w
    
    # Check pod status
    kubectl get pods
    
    # Clean up test
    kubectl delete deployment nginx-test
    ```

## Troubleshooting

- If `kubectl cluster-info` fails, check AWS credentials and region
- If no nodes appear after deploying workloads, check `kubectl get events` for errors
- Nodes terminate when no longer needed (cost optimization)