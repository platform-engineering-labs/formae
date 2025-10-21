FROM ubuntu:latest

ARG VERSION=latest

RUN useradd -m -s /bin/bash pel
RUN apt-get update &&  \
    apt-get install -y jq && \
    /bin/bash -c "$(curl -fsSL https://hub.platform.engineering/setup/formae.sh)" -- -y -v ${VERSION} && \
    apt-get remove -y jq && \
    apt-get autoremove -y --purge && \
    apt-get clean

USER pel
WORKDIR /home/pel
ENV PATH=/opt/pel/formae/bin:$PATH

EXPOSE 49684

CMD ["formae", "agent", "start"]