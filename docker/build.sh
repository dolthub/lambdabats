#!/bin/bash

set -e
set -o pipefail

(cd ../server && GOOS=linux GOARCH=arm64 go build -o ../docker/server .)
docker build .
