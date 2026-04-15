FROM ubuntu:latest

ARG VERSION
ARG CHANNEL="stable"

RUN if [ -z "$VERSION" ]; then echo "VERSION is required"; exit 1; fi

ENV PATH=/opt/pel/bin:$PATH

RUN useradd -m -s /bin/bash pel
RUN apt-get update &&  \
    apt-get install -y jq curl && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes --channel ${CHANNEL} formae@${VERSION} && \
    apt-get remove -y jq curl && \
    apt-get autoremove -y --purge && \
    apt-get clean && \
    /opt/pel/bin/formae clean --all

RUN chown -R pel:pel /home/pel

USER pel
WORKDIR /home/pel

EXPOSE 49684

CMD ["formae", "agent", "start"]