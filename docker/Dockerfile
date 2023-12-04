FROM public.ecr.aws/lambda/provided:al2023.2023.11.18.01

ADD bats-core-1.10.0.tar.gz /
RUN /bats-core-1.10.0/install.sh /usr/local && dnf -y install tar nc procps diffutils findutils expect python3 binutils && pip install mysql-connector-python

ADD bash /bin/bash
ADD server /
ADD run.sh /

ENTRYPOINT ["/run.sh"]
