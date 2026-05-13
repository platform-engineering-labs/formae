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
#
# Install runs as root so /opt/pel ends up root-owned — the canonical
# system-install layout. The agent process itself runs as `pel`
# (unprivileged) and only reads from /opt/pel; runtime plugin
# install/update/uninstall is performed via the `formae plugin ...` CLI,
# which prompts for sudo when writing to the root-owned plugin store.
RUN apt-get update &&  \
    apt-get install -y jq curl && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes --channel ${CHANNEL} formae@${VERSION} && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes --channel stable standard && \
    HOME=/home/pel /bin/bash -e -c "$(curl -fsSL https://hub.platform.engineering/get/setup.sh)" -- install --yes --channel stable aws@0.1.7 && \
    apt-get remove -y jq curl && \
    apt-get autoremove -y --purge && \
    apt-get clean && \
    /opt/pel/bin/formae clean --all

# The installer ran as root with HOME=/home/pel, so anything written
# under /home/pel (pelmgr config, caches, etc.) needs ownership fixed
# so the agent — which runs as `pel` — can read and write its data
# directory at /home/pel/.pel/formae.
RUN chown -R pel:pel /home/pel

USER pel
WORKDIR /home/pel

EXPOSE 49684

CMD ["formae", "agent", "start"]