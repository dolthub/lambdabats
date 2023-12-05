%define _trivial	.0
%define _buildid	.0

%define patchlevel 0
%define baseversion 1.10

Version: %{baseversion}.%{patchlevel}
Name: bats
Summary: Bash Automated Testing System
Release: 1%{?dist}%{?_trivial}%{?_buildid}
License: MIT
Url: https://github.com/bats-core/bats-core
Source0: bats-core-%{baseversion}.%{patchlevel}.tar.gz

BuildRequires: bash
Requires: filesystem >= 3
Requires: bash
Provides: /usr/bin/bats

%description
Bats is a TAP-compliant testing framework for Bash. It provides a simple way to
verify that the UNIX programs you write behave as expected.

%prep
tar zxf ../SOURCES/bats-core-%{baseversion}.%{patchlevel}.tar.gz
cd bats-core-%{baseversion}.%{patchlevel}

%build

%install

cd bats-core-%{baseversion}.%{patchlevel}
./install.sh %{buildroot}/usr

%check

%files
%{_bindir}/bats
%dir %{_bindir}/../libexec/bats-core
%{_bindir}/../libexec/bats-core/*
%dir %{_bindir}/../lib/bats-core
%{_bindir}/../lib/bats-core/*
%{_mandir}/*/*

%changelog
* Tue Dec 5 2023 Aaron Son <aaron@dolthub.com> - 1.10.0-0.amzn2023.0.1
- Initial build.
