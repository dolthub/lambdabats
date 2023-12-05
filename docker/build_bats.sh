#!/bin/bash

set -e

cd
curl -o rpmbuild/SOURCES/bats-core-1.10.0.tar.gz -L https://github.com/bats-core/bats-core/archive/refs/tags/v1.10.0.tar.gz

cd rpmbuild/SPECS
find /home/builder/rpmbuild/SOURCES/

rpmbuild -ba bats.spec

# /home/builder/rpmbuild/RPMS/aarch64/bats-1.10.0-1.amzn2023.0.0.aarch64.rpm
