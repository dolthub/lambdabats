lambdabats
==========

lambdabats is a test runner for the `bats` tests in the Dolt GitHub repository,
https://github.com/dolthub/dolt/tree/main/integration-tests/bats.

Getting Started
---------------

Install lambdabats by running:

```sh
$ go install .
```

in this directory.

Before using lambdabats, and periodically throughout the day, you may need to
refresh AWS credentials. You can do so with:

```sh
$ lambdabats login
```

which will open a browser and put you through an SSO login flow involving your
DoltHub Google Workspace account.

After this, you can run lambdabats similarly to how you would run bats itself.
For example:

```sh
$ pwd
~/src/dolt/integration-tests/bats
$ lambdabats .
building dolt... done
building remotesrv... done
building bats.tar... done
uploading tests to s3 100% |█████████████████████████| (204/204 MB, 194 MB/s)        
running tests 100% |█████████████████████████████████| (2692/2692, 40 it/s)          
1pk5col-ints.bats                                                                                                                                                                                         
  ✓ 1pk5col-ints: empty table
  ✓ 1pk5col-ints: create a table, dolt add, dolt reset, and dolt commit
  ✓ 1pk5col-ints: add a row to a created table using dolt table put-row
  ✓ 1pk5col-ints: dolt sql all manner of inserts
  ✓ 1pk5col-ints: dolt sql insert same column twice
  ✓ 1pk5col-ints: dolt sql insert no columns specified
  ✓ 1pk5col-ints: dolt sql with insert ignore
  ✓ 1pk5col-ints: dolt sql replace into
  ✓ 1pk5col-ints: dolt sql insert and dolt sql select
  ✓ 1pk5col-ints: dolt sql select as
...
  ✓ verify-constraints: CLI --output-only
  ✓ verify-constraints: CLI --all and --output-only

window.bats
  ✓ window: no diff table window buffer sharing

2692 tests, 0 failures, 151 skipped
```

How It Works
------------

`lambdabats` works with an alread-provisioned Lambda function that contains all
the dependencies of the `bats` tests. When you invoke it, it:

1) Builds `dolt` in the same repository as the tests are in.

2) Builds `remotesrv` in the same repository as the tests are in.

3) Tars up the entire contents of the `bats` directory.

4) Uploads the artifacts from #1, #2, #3 to an S3 bucket.

5) Enumerates all the bats tests passed as arguments to `lambdabats`.

6) For each enumerated test, invokes the Lambda function, instructing it to run the test.

7) Assembles all the test results and outputs them.

Caveats & Going Further
-----------------------

There are a few `bats` tests which `lambdabats` cannot run in Lambda. By
default, `lambdabats` runs those tests locally instead and assembles their
results into the test run output. You can supply a different execution strategy
by passing `-s lambda_skip` when you invoke `lambdabats`. This will cause it to
skip those tests instead.

`lambdabats` uses [bats test
tags](https://bats-core.readthedocs.io/en/stable/writing-tests.html#tagging-tests)
to decide to run a test locally. In particular, it looks for the test tag
`no_lambda`, set with the syntax `# bats test_tags=no_lambda` on its own line
before the definition of the test.

`lambdabats` does not actually have a robust parser for extracting tests or
their tags from the `bats` test files. It only supports a subset of the `@test`
format for defining tests and it requires whitespace in specific places for
detecting the test tags. Contributions welcome.

If you want to run the tests remotely with an environment variable set, you can
the `--env` flag.  For example, run `lambdabats --env SQL_ENGINE=remote-engine
.` to set `SQL_ENGINE` in the remote (and local) invocations. You can pass
multiple environment variables this way. `HOME`, `PATH` and `TMPDIR` are not
settable this way (at least for the the remote invocation).

Currently when `lambdabats` runs tests locally, it does nothing to administor
the local environment in which the tests run. That means, it does not build
`dolt` or `remotesrv` for the host platform and put them on the path, it does
not create a python environment with mysql-connector-python, it does not strive
to have the Parquet CLI available, etc. It instead behaves similarly to how
`bats` would be behave if you ran `bats ...` locally.

You can make `lambdabats` output TAP style results with `lambdabats -F tap
...`. It does not currently support tap13, junit, or other output formats
besides the `bats` default and `tap`.

Currently we don't do anything to make different versions of the pre-installed
dependencies available in the Lambda function. There is only one version of the
function which we invoke at a time.

Currently in order to change the pre-installed dependencies in the Lambda
function, you need to contact an administrator, like dustin@ or aaron@.

Currently `lambdabats` does not support filtering of which tests to run within
a file, including any syntax to only run tests with certain tags or tests
matching a certain filter regex. `lambdabats` does not currently understand
`bats:focus`.

Currently `lambdabats` does not attempt to do any of the following:

1) Hedge a slow test run, in an attempt to get around a straggler.

2) Rerun a failed test, in an attempt to detect flakiness vs. consistent brokeness.

3) Detect certain Lambda function errors and automatically retry the test run, such as a timeout, instead of treating it as a test failure.
