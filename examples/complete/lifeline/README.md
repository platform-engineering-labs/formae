# Complete / Lifeline

This is the showcase that demonstrates how to work with **formae** from Day 0 to Day N.

We start with the initial rollout of the infrastructure. After the initial infrastructure is ready,
we show how to make holistic changes to it.

In the next step, we show how to apply micro changes with the overall minimum blast radius.

And the last step of the example shows how a team responsible for cross-cutting changes can apply changes on significant
portions of the infrastructure, but each of the changes has a minimum blast radius.

## Basic infrastructure

The forma `basic_infrastructure.pkl` is supposed to be used to create, replace or destroy the basic infrastructure,
and it is intended to be applied holistically, for example in a GitOps-oriented setting.
'Holistically' means applying this forma in replace (default) mode. Typically, this form will be
held and versioned in a Git repository, although **formae** can also make it obsolete to use Git.

This forma will be used by one of the core platform engineers who are responsible for providing infrastructure
to the development teams to prepare the foundation on which the teams will apply minimal changes with
minimal blast radius, for example workload deployments, changing memory and CPU requirements etc.

It is also used as foundation for enabling less experienced platform engineers with less responsibility
to make minimal basic infrastructure changes with minimal blast radius, for example adding a new subnet.

Apply this forma initially in the replace (default) mode to create the basic infrastructure.
Or run it again with a different AWS account or region to create the same basic infrastructure in a different
environment.

Make changes to this forma to add, change or remove resources holistically by applying it in replace (default) mode again.

Take into account that a resource that is missing in the new version of this forma will be destroyed in the replace
mode, if present.

Destroy all resources referenced by this forma using the destroy command.

### Initial setup

Ensure the **formae** node is up and running. Then, in a different shell, run:

`formae apply --watch /opt/pel/formae/examples/complete/lifeline/basic_infrastructure.pkl`

Once the asynchronous command is done, you can see in your AWS console that a few resources have been created, for example a VPC with a name starting with `lifeline`, and more network related resources.

### Another holistic apply

Let's say, some time later, we want to add another security group following our GitOps process.

In `security_groups.pkl`, which is part of the basic infrastructure, you should uncomment the initially commented security group and its corresponding ingress at the end of the file, and then run again:

`formae apply --watch /opt/pel/formae/examples/complete/lifeline/basic_infrastructure.pkl`

Once the asynchronous command is done, you should see in your AWS console that another security group with a name starting with `lifeline` has been created.

We are now done with the basic infrastructure.

## Micro-Changes

Forma calles `micro_change.pkl` is supposed to be used in patch mode and demonstrate how to make minimal changes
to the infrastructure with minimal blast radius. This is an example of how less experienced
platform engineers and developer teams can make changes to the infrastructure without seeing
or touching everything else. Another use case is to make urgent changes, for example at night,
without having to deal with all details. And yet another use case would be developers changing
some parameters in their deployments, such as memory and CPU requirements.

Apply this forma in patch mode to create a new S3 bucket.

Destroy all resources referenced by this forma using the destroy command.

### Apply the micro-change

Ensure the **formae** node is up and running. Then, in a different shell, run:

`formae apply --watch --mode patch /opt/pel/formae/examples/complete/lifeline/micro_change.pkl`

Once the asynchronous command is done, you can see in your AWS console that an S3 bucket with a name starting with `lifeline` has been created, with a few tags set on it.

## Cross-cutting change

Forma called `cross-cutting_change.pkl` is supposed to be used in patch mode and demonstrate how to make minimal
cross-cutting changes. Typical use cases would be to harden systems, adding consistent
metadata, compliance, cost and performance optimizations and similar. Anyone in the team
should be able to apply this forma, but its sweet spot is to be used by security engineers,
consultants and similar roles.

Apply this forma in patch mode to add a specific tag to multiple existing resources.

One can run destroy using this forma, but it shouldn't be done.

### Apply the cross-cutting change

Ensure the **formae** node is up and running. Then, in a different shell, run:

`formae apply --watch --mode patch /opt/pel/formae/examples/complete/lifeline/cross_cutting_change.pkl`

Once the asynchronous command is done, you can see in your AWS console that a few resources with names starting with `lifeline` have been updated, with a new tag set on them.

## Destroy what has been created and updated

Ensure the **formae** node is up and running. Then, in a different shell, run:

```
formae destroy --watch --mode patch --forma-file /opt/pel/formae/examples/complete/lifeline/micro_change.pkl`
formae destroy --watch --mode patch --forma-file /opt/pel/formae/examples/complete/lifeline/basic_infrastructure.pkl`
```

In your AWS console, you shouldn't see any resources with names starting with `lifeline` anymore.
