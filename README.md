lambdabats
==========

`lambdabats` is machinery for running Dolt `bats` tests with massive
parallelism taking advantage of AWS Lambda.

The client, which Dolt developers may be interested in, lives in
[./lambdabats](./lambdabats).

The server lives in [./server](./server), and some scripts for building the
Docker image which makes up the Lambda function live in [./docker](./docker).
