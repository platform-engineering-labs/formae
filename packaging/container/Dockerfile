FROM ubuntu:latest

ARG VERSION=latest
ARG FORMAE_ARTIFACT_USERNAME
ARG FORMAE_ARTIFACT_PASSWORD

RUN useradd -m -s /bin/bash pel
RUN apt-get update &&  \
    apt-get install -y jq curl && \
    FORMAE_ARTIFACT_USERNAME=${FORMAE_ARTIFACT_USERNAME} \
    FORMAE_ARTIFACT_PASSWORD=${FORMAE_ARTIFACT_PASSWORD} \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/setup/formae.sh)" -- -y -v ${VERSION} && \
    apt-get remove -y jq curl && \
    apt-get autoremove -y --purge && \
    apt-get clean

RUN chown -R pel:pel /home/pel

USER pel
WORKDIR /home/pel
ENV PATH=/opt/pel/formae/bin:$PATH

EXPOSE 49684

CMD ["formae", "agent", "start"]
