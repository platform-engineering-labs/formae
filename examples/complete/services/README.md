# Complete / Services

This example contains two sub-examples: high-level provisioning of a database and of a database service (among potentially others).

## Database

Typically, platform team provides reusable infrastructure components for developers to spin up as quickly and simple as possible - such as databases.
The first example shows how to do it. All the detail hidden from the end user (developer) and is managed by the platform team.

Familiarize yourself with the provided forma `database.pkl`. It opens only two properties for configuration. Run the following command to list them:

`formae eval --help /opt/pel/formae/examples/complete/services/services.pkl`

At the end of the output, it will list the available forma properties:

`team` - a required property, the user has to set it to one of the possible values constrained in `types.pkl` - team-a or team-b
`size` - an optional flag for the t-shirt size of the database. Default would be "xs", with "s" being the only other option described in `types.pkl`

Running the `apply` command will require the team flag to be set. The resulting infrastructure contains all kind of VPC related resources, and an RDS database instance.

Destroying is best done running `formae destroy --query="stack:team-a-stack"` (or team-b, depending on what team was selected). Alternatively run:

`formae destroy /opt/pel/formae/examples/complete/services/services.pkl` by providing the flag `--team`.

## Services

On a higher level of abstraction, the platform team provides developers with reusable services, so that they can pick from a catalog. The example `services.pkl` shows how
to do it. Again, familiarize yourself with the available properties before running any real commands. These properties are available:

`team` - a required property, the user has to set it to one of the possible values constrained in `types.pkl` - team-a or team-b
`database` - an optional flag that defaults to false. Set it to true to provision a database service
`database-size` - an optional flag for the t-shirt size of the database. Default would be "xs", with "s" being the only other option described in `types.pkl`

Applying and destroying is similar to the above example.

Starting from here, the platform team can add other services to choose from: queue, load-balancer etc.