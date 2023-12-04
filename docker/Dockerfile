FROM --platform=linux/arm64 public.ecr.aws/lambda/provided:al2023.2023.11.18.01

RUN dnf -y install tar nc procps diffutils findutils expect python3 binutils java-17-amazon-corretto-headless && pip install mysql-connector-python
ENV JAVA_HOME=/usr/lib/jvm/java-17-amazon-corretto.aarch64/

RUN cd /opt && curl -OL https://downloads.apache.org/hadoop/common/hadoop-3.3.4/hadoop-3.3.4.tar.gz && tar zxf hadoop-3.3.4.tar.gz && rm -f hadoop-3.3.4.tar.gz
ENV PATH=/opt/hadoop-3.3.4/bin:"$PATH"
RUN mkdir /opt/parquet-1.12.1 && cd /opt/parquet-1.12.1 && curl -OL https://repo1.maven.org/maven2/org/apache/parquet/parquet-cli/1.12.1/parquet-cli-1.12.1-runtime.jar
ENV PARQUET_RUNTIME_JAR=/opt/parquet-1.12.1/parquet-cli-1.12.1-runtime.jar

# TODO: Move this up to first line when we have to rebuild with the hadoop download again.
RUN pip install pyarrow pandas

ADD bats-core-1.10.0.tar.gz /
RUN /bats-core-1.10.0/install.sh /usr/local

ADD bash /bin/bash
ADD server /
ADD run.sh /

ENTRYPOINT ["/run.sh"]
