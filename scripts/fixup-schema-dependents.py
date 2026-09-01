#!/usr/bin/env python3
# © 2026 Platform Engineering Labs Inc.
#
# SPDX-License-Identifier: FSL-1.1-ALv2

"""Refresh dependent plugin schemas after a formae schema publish.

The schema coordinate formae@X.Y.Z is deliberately mutable: every dev build on
the X.Y.Z line republishes it. A plugin schema package built against it bakes
the then-current zip checksum into its own published metadata
(dependencies.formae.checksums.sha256), and `pkl project resolve` rejects any
dependency whose served zip no longer matches a declared checksum. A formae
schema publish therefore invalidates every dependent plugin schema published
before it, and has to bring their declared checksums back in line as part of
the same publish.

Usage: fixup-schema-dependents.py <schema-version>

Expects the freshly packaged metadata at .out/formae@<version>/formae@<version>
(the new checksum is read from it, exactly as pkl computed it) and AWS
credentials able to read and write the hub bucket.
"""

import json
import re
import subprocess
import sys
import tempfile

BUCKET = "hub.platform.engineering"

# Plugin schema metadata objects: plugins/<plugin>/schema/pkl/<name>/<name>@<version>,
# extensionless, with the package zip sitting next to them.
METADATA_KEY = re.compile(r"^plugins/[^/]+/schema/pkl/[^/]+/[^/]+@[0-9][^/]*$")


def aws(*args: str) -> str:
    return subprocess.run(
        ["aws", *args], check=True, capture_output=True, text=True
    ).stdout


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit(f"usage: {sys.argv[0]} <schema-version>")
    version = sys.argv[1]
    coordinate = f"package://{BUCKET}/plugins/pkl/schema/pkl/formae/formae@{version}"

    with open(f".out/formae@{version}/formae@{version}") as f:
        sha256 = json.load(f)["packageZipChecksums"]["sha256"]
    print(f"formae@{version} zip sha256: {sha256}")

    listing = aws(
        "s3api", "list-objects-v2",
        "--bucket", BUCKET,
        "--prefix", "plugins/",
        "--query", "Contents[].Key",
        "--output", "text",
    )
    keys = [
        key
        for key in listing.split()
        if METADATA_KEY.match(key)
        and not key.endswith(".zip")
        and not key.endswith(".sha256")
    ]

    refreshed = 0
    for key in keys:
        with tempfile.NamedTemporaryFile("r+", suffix=".json") as tmp:
            aws("s3", "cp", f"s3://{BUCKET}/{key}", tmp.name, "--only-show-errors")
            try:
                doc = json.load(tmp)
            except ValueError:
                print(f"skipping {key}: not package metadata")
                continue
            stale = [
                dep
                for dep in doc.get("dependencies", {}).values()
                if dep.get("uri") == coordinate
                and dep.get("checksums", {}).get("sha256") != sha256
            ]
            if not stale:
                continue
            for dep in stale:
                dep["checksums"] = {"sha256": sha256}
            tmp.seek(0)
            tmp.truncate()
            json.dump(doc, tmp, indent=2)
            tmp.flush()
            aws("s3", "cp", tmp.name, f"s3://{BUCKET}/{key}", "--only-show-errors")
            print(f"refreshed {key}")
            refreshed += 1

    print(f"{refreshed} dependent(s) refreshed, {len(keys)} schema package(s) checked")


if __name__ == "__main__":
    main()
