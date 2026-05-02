FROM ubuntu:latest

ARG VERSION
ARG CHANNEL="stable"

RUN if [ -z "$VERSION" ]; then echo "VERSION is required"; exit 1; fi

ENV PATH=/opt/pel/bin:$PATH

RUN useradd -m -s /bin/bash pel

# Install formae + the standard metapackage. The metapackage's `requires`
# resolve at install time and pull in the curated default plugin set
# (aws, azure, gcp, oci, ovh, auth-basic) — replacing the legacy bundled-
# plugins-from-binary extraction step.
RUN apt-get update &&  \
    apt-get install -y jq curl && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes --channel ${CHANNEL} formae@${VERSION} standard && \
    apt-get remove -y jq curl && \
    apt-get autoremove -y --purge && \
    apt-get clean && \
    /opt/pel/bin/formae clean --all

# Make the install tree owned by `pel`. Orbital's tree library considers any
# path owned by uid 0 "Privileged" and tries to escalate via sudo on every
# read/write — but the image runs as `pel` and sudo is not installed, so the
# agent fails to start with:
#
#   privileged user required to manage path: /opt/pel
#   Plugin manager initialization failed; refusing to start
#   exec: "sudo": executable file not found in $PATH
#
# Once /opt/pel is owned by `pel`, orbital's privileged() check returns false,
# no sudo is invoked, and runtime plugin install/upgrade/uninstall continue
# to work because new files inherit the pel ownership.
RUN chown -R pel:pel /home/pel /opt/pel

USER pel
WORKDIR /home/pel

EXPOSE 49684

CMD ["formae", "agent", "start"]