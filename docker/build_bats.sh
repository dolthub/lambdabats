#!/bin/bash

set -e

cd
curl -o rpmbuild/SOURCES/bats-core-1.11.1.tar.gz -L https://github.com/bats-core/bats-core/archive/refs/tags/v1.11.1.tar.gz

cd rpmbuild/SPECS
find /home/builder/rpmbuild/SOURCES/

rpmbuild -ba bats.spec

# /home/builder/rpmbuild/RPMS/aarch64/bats-1.11.1-1.amzn2023.0.0.aarch64.rpm
