#!/bin/bash

set -e

cd

yumdownloader --source bash
rpm -i bash-5.2.15-1.amzn2023.0.2.src.rpm
cd rpmbuild/SPECS

sed \
  -e '2c %define _buildid	.2.1' \
  -e '135c %configure --with-bash-malloc=no --with-afs bash_cv_dev_fd=whacky' \
  -e '323a * Sat Dec 4 2023 Aaron Son <aaron@dolthub.com> - 5.2.15-1.amzn2023.0.2.1\
- Build with bash_cv_dev_fd=whacky so we look in /proc/self/fd/*. Makes it work with Lambda.\
' -i.orig bash.spec

rpmbuild -ba --nocheck bash.spec

#/home/builder/rpmbuild/RPMS/aarch64/bash-5.2.15-1.amzn2023.0.2.1.aarch64.rpm
#/home/builder/rpmbuild/RPMS/aarch64/bash-debuginfo-5.2.15-1.amzn2023.0.2.1.aarch64.rpm
#/home/builder/rpmbuild/RPMS/aarch64/bash-debugsource-5.2.15-1.amzn2023.0.2.1.aarch64.rpm
#/home/builder/rpmbuild/RPMS/aarch64/bash-devel-5.2.15-1.amzn2023.0.2.1.aarch64.rpm
#/home/builder/rpmbuild/RPMS/aarch64/bash-doc-5.2.15-1.amzn2023.0.2.1.aarch64.rpm
#
#/home/builder/rpmbuild/SRPMS/bash-5.2.15-1.amzn2023.0.2.1.src.rpm
