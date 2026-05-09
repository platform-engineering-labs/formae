FROM ubuntu:latest

ARG VERSION
ARG CHANNEL="stable"

RUN if [ -z "$VERSION" ]; then echo "VERSION is required"; exit 1; fi

ENV PATH=/opt/pel/bin:$PATH

RUN useradd -m -s /bin/bash pel

# Two-step install. The formae binary lives in the pel repo at the
# release's CHANNEL (stable for X.Y.Z, dev for X.Y.Z-dev[.N]); the
# standard metapackage and its constituent plugins always come from
# community#stable. Plugins are released independently of formae and
# don't carry a parallel dev channel — even dev formae containers ship
# with stable plugins. Pelmgr applies one channel per install call, so
# they have to be separate invocations.
#
# Standard's `requires` resolve at install time and pull in the curated
# default plugin set (aws, azure, gcp, oci, ovh, auth-basic) —
# replacing the legacy bundled-plugins-from-binary extraction step.
RUN apt-get update &&  \
    apt-get install -y jq curl && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes --channel ${CHANNEL} formae@${VERSION} && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes --channel stable standard && \
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