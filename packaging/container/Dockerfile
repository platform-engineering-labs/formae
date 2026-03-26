FROM ubuntu:latest

ARG VERSION=latest

RUN useradd -m -s /bin/bash pel
RUN apt-get update &&  \
    apt-get install -y jq curl && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/setup/formae.sh)" -- -y -v ${VERSION} && \
    apt-get remove -y jq curl && \
    apt-get autoremove -y --purge && \
    apt-get clean

# Temp Fix: Trigger plugin migration so resource plugins are baked into the image.
RUN PATH=/opt/pel/formae/bin:$PATH HOME=/home/pel \
    /opt/pel/formae/bin/formae agent start >/dev/null 2>&1 & sleep 5 && \
    /opt/pel/formae/bin/formae agent stop >/dev/null 2>&1 || true && \
    chown -R pel:pel /home/pel/.pel && \
    test -d /home/pel/.pel/formae/plugins && \
    test "$(ls -A /home/pel/.pel/formae/plugins)" || \
    (echo "ERROR: plugin migration failed" && exit 1)

USER pel
WORKDIR /home/pel
ENV PATH=/opt/pel/formae/bin:$PATH

EXPOSE 49684

CMD ["formae", "agent", "start"]
