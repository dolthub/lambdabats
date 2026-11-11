#!/bin/bash

set -euo pipefail

# Resolve paths relative to this script, so it can be run from any directory.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

(cd "$script_dir/../server" && GOOS=linux GOARCH=arm64 go build -o "$script_dir/server" .)
docker build --platform=linux/arm64 -t 407903926827.dkr.ecr.us-west-2.amazonaws.com/dolt_lambda_bats_runner "$script_dir"
