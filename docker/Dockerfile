# The Lambda runtime base image, and the Amazon Linux 2023 image for the same
# release, which we build RPMs in because the Lambda image only has microdnf.
# The bash we rebuild must come from the same release as the bash it replaces.
# Bump these together: `cat /etc/amazon-linux-release` in the Lambda image
# shows its release, and the build fails if the two releases differ.
ARG LAMBDA_BASE_IMAGE=public.ecr.aws/lambda/provided:al2023.2026.11.10.21
ARG AMAZON_LINUX_IMAGE=public.ecr.aws/amazonlinux/amazonlinux:2023.12.20261026.1

FROM ${AMAZON_LINUX_IMAGE} AS rpms

RUN yum -y groupinstall "Development Tools" && \
  yum -y install yum-utils glibc-all-langpacks ncurses-devel texinfo && \
  useradd -m builder

ADD build_bash.sh /
RUN su -l builder -c /build_bash.sh

ADD bats.spec /home/builder/rpmbuild/SPECS/bats.spec
ADD build_bats.sh /
RUN su -l builder -c /build_bats.sh

FROM ${LAMBDA_BASE_IMAGE}

RUN dnf -y install \
  binutils \
  diffutils \
  expect \
  findutils \
  git \
  java-17-amazon-corretto-headless \
  nc \
  procps \
  python3 \
  tar \
  && \
  pip install \
    mysql-connector-python \
    pyarrow \
    pandas

ENV JAVA_HOME=/usr/lib/jvm/java-17-amazon-corretto.aarch64/

# The same Hadoop build Dolt CI uses. Its checksum is the one Apache publishes.
RUN cd /opt && \
  curl -fsSLO https://dolthub-tools.s3.us-west-2.amazonaws.com/hadoop-3.3.6.tar.gz && \
  echo "de3eaca2e0517e4b569a88b63c89fae19cb8ac6c01ff990f1ff8f0cc0f3128c8e8a23db01577ca562a0e0bb1b4a3889f8c74384e609cd55e537aada3dcaa9f8a  hadoop-3.3.6.tar.gz" | sha512sum -c - && \
  tar zxf hadoop-3.3.6.tar.gz && \
  rm -f hadoop-3.3.6.tar.gz
ENV PATH=/opt/hadoop-3.3.6/bin:"$PATH"

RUN curl -fsSL https://github.com/krallin/tini/releases/download/v0.19.0/tini-arm64 -o /usr/local/bin/tini && \
  echo "07952557df20bfd2a95f9bef198b445e006171969499a1d361bd9e6f8e5e0e81  /usr/local/bin/tini" | sha256sum -c - && \
  chmod +x /usr/local/bin/tini

RUN mkdir /opt/parquet-1.12.3 && cd /opt/parquet-1.12.3 && \
  curl -fsSLO https://repo1.maven.org/maven2/org/apache/parquet/parquet-cli/1.12.3/parquet-cli-1.12.3-runtime.jar && \
  echo "feb318ce9fe78ac516dbf6cf9227cfef4f6606e72fce2bfdb601223807ed6265  parquet-cli-1.12.3-runtime.jar" | sha256sum -c -
ENV PARQUET_RUNTIME_JAR=/opt/parquet-1.12.3/parquet-cli-1.12.3-runtime.jar

COPY --from=rpms /etc/amazon-linux-release /root/rpms/amazon-linux-release
COPY --from=rpms /home/builder/rpmbuild/RPMS/aarch64/bats-[0-9]*.rpm /home/builder/rpmbuild/RPMS/aarch64/bash-[0-9]*.rpm /root/rpms/

RUN if [ "$(cat /etc/amazon-linux-release)" != "$(cat /root/rpms/amazon-linux-release)" ]; then \
    echo "RPMs were built on $(cat /root/rpms/amazon-linux-release), but the Lambda base image is $(cat /etc/amazon-linux-release)." >&2; \
    exit 1; \
  fi && \
  rpm -i /root/rpms/bats-[0-9]*.rpm && rpm -U /root/rpms/bash-[0-9]*.rpm

ADD server /
ADD run.sh /

ENTRYPOINT ["/usr/local/bin/tini", "/run.sh"]
