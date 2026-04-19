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

# Trigger plugin migration so resource plugins are baked into the image.
# Run as the pel user so files/pid files are created with correct ownership.
# Running as root would leave a root-owned /tmp/formae.pid that the pel user
# cannot read or delete at container runtime, breaking agent startup.
RUN su - pel -c "PATH=/opt/pel/bin:$PATH HOME=/home/pel /opt/pel/bin/formae agent start >/dev/null 2>&1 & sleep 5 && /opt/pel/bin/formae agent stop >/dev/null 2>&1 || true" && \
    test -d /home/pel/.pel/formae/plugins && \
    test "$(ls -A /home/pel/.pel/formae/plugins)" || \
    (echo "ERROR: plugin migration failed" && exit 1)

USER pel
WORKDIR /home/pel

EXPOSE 49684

CMD ["formae", "agent", "start"]