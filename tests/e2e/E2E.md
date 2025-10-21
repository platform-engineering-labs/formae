# E2E

## How to add e2e tests to the project?

Run your forma locally and make sure to copy over the stack by using the followign assumptions:

- E.g. you want to add a test named `s3-bucket` to the e2e tests.
- Make sure to name the stack `s3-bucket-stack`. Copy over the state file to [expected](./expected/) with the name `s3-bucket-stack.json`.
- Add your tests to [e2e-test-definitions.yaml](config/e2e-test-definitions.yaml) with the name `s3-bucket`. Put in the file to `source` pkl file and set the `expected_state` to the copied over state file. (copying over an existing test makes it easier)
- You can run it locally by `make full-e2e` from the root of the project.