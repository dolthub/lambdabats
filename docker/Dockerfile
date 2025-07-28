FROM public.ecr.aws/amazonlinux/amazonlinux:2023 AS rpms

RUN yum -y groupinstall "Development Tools" && \
  yum -y install yum-utils glibc-all-langpacks ncurses-devel texinfo && \
  useradd -m builder

ADD build_bash.sh /
RUN su -l builder -c /build_bash.sh

ADD bats.spec /home/builder/rpmbuild/SPECS/bats.spec
ADD build_bats.sh /
RUN su -l builder -c /build_bats.sh

FROM public.ecr.aws/lambda/provided:al2023.2025.07.28.10

RUN dnf -y install \
  tar \
  nc \
  procps \
  diffutils \
  findutils \
  expect \
  python3 \
  binutils \
  java-17-amazon-corretto-headless && \
  pip install \
    mysql-connector-python \
    pyarrow \
    pandas

ENV JAVA_HOME=/usr/lib/jvm/java-17-amazon-corretto.aarch64/

RUN cd /opt && curl -OL https://downloads.apache.org/hadoop/common/hadoop-3.3.6/hadoop-3.3.6.tar.gz && tar zxf hadoop-3.3.6.tar.gz && rm -f hadoop-3.3.6.tar.gz
ENV PATH=/opt/hadoop-3.3.6/bin:"$PATH"

RUN mkdir /opt/parquet-1.12.3 && cd /opt/parquet-1.12.3 && curl -OL https://repo1.maven.org/maven2/org/apache/parquet/parquet-cli/1.12.3/parquet-cli-1.12.3-runtime.jar
ENV PARQUET_RUNTIME_JAR=/opt/parquet-1.12.3/parquet-cli-1.12.3-runtime.jar

COPY --from=rpms /home/builder/rpmbuild/RPMS/aarch64/bats-1.10.0-1.amzn2023.0.0.aarch64.rpm /root/bats-1.10.0-1.amzn2023.0.0.aarch64.rpm
COPY --from=rpms /home/builder/rpmbuild/RPMS/aarch64/bash-5.2.15-1.amzn2023.0.2.1.aarch64.rpm /root/bash-5.2.15-1.amzn2023.0.2.1.aarch64.rpm

RUN rpm -i /root/bats-1.10.0-1.amzn2023.0.0.aarch64.rpm && rpm -U /root/bash-5.2.15-1.amzn2023.0.2.1.aarch64.rpm

ADD server /
ADD run.sh /

ENTRYPOINT ["/run.sh"]
