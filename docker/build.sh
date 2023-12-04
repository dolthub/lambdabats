#!/bin/bash

set -e
set -o pipefail

(cd ../server && GOOS=linux GOARCH=arm64 go build -o ../docker/server .)
docker build -t 407903926827.dkr.ecr.us-west-2.amazonaws.com/dolt_lambda_bats_runner .
