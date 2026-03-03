FROM ubuntu:latest

ARG VERSION=latest

RUN useradd -m -s /bin/bash pel
RUN apt-get update &&  \
    apt-get install -y jq curl file sudo && \
    /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/setup/formae.sh)" -- -y -v ${VERSION} && \
    apt-get remove -y jq curl file && \
    SUDO_FORCE_REMOVE=yes apt-get remove -y sudo && \
    apt-get autoremove -y --purge && \
    apt-get clean
RUN mkdir -p /home/pel/.pel/formae/plugins && \
    cp -r /root/.pel/formae/plugins/* /home/pel/.pel/formae/plugins/ && \
    chown -R pel:pel /home/pel/.pel

USER pel
WORKDIR /home/pel
ENV PATH=/opt/pel/formae/bin:$PATH

EXPOSE 49684

CMD ["formae", "agent", "start"]