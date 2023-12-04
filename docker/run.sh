#!/bin/sh
if [ -z "${AWS_LAMBDA_RUNTIME_API}" ]; then
  export USE_LOCAL_DOWNLOADER=1
  exec /usr/local/bin/aws-lambda-rie /server "$@"
else
  exec /server "$@"
fi
