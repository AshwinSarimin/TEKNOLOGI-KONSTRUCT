#!/usr/bin/env python3
"""Pin every deployed Promise image to ACR digests from one source commit.

Usage: python3 scripts/pin_promise_images.py --commit <40-character Git SHA>
The commit-tagged images must already exist in teknologieur1acr. This script
updates the Promise manifests, notify-watcher deployment, and release lock.
"""

import argparse
import json
import pathlib
import re
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[1]
MANIFESTS = (
    "promises/namespace/promise.yaml",
    "promises/keyvault/promise.yaml",
    "promises/storage-account/promise.yaml",
    "promises/storage-account-terraform/promise.yaml",
    "promises/team-onboarding/promise.yaml",
    "tenants/platform/notify-watcher/base/configs/deployment.yaml",
)
IMAGES = (
    "namespace", "keyvault", "storage-account", "storage-account-terraform",
    "team-onboarding", "ownership", "notify",
)
IMAGE_PATTERN = re.compile(
    r"(?P<prefix>teknologieur1acr\.azurecr\.io/kratix-(?P<name>[a-z0-9-]+))"
    r"(?P<reference>:(?:latest|[0-9a-f]{40})|@sha256:[0-9a-f]{64})"
)
DIGEST_PATTERN = re.compile(r"sha256:[0-9a-f]{64}\Z")
COMMIT_PATTERN = re.compile(r"[0-9a-f]{40}\Z")


def validate_digests(digests):
    if set(digests) != set(IMAGES):
        raise ValueError(f"expected exactly these images: {', '.join(IMAGES)}")
    for name, digest in digests.items():
        if not DIGEST_PATTERN.fullmatch(digest):
            raise ValueError(f"invalid digest for {name}: {digest!r}")


def read_lock(path):
    lock = json.loads(path.read_text())
    if not COMMIT_PATTERN.fullmatch(lock.get("sourceCommit", "")):
        raise ValueError("lock has no full sourceCommit")
    validate_digests(lock["images"])
    return lock


def digests_from_lock(path, commit):
    lock = read_lock(path)
    if lock["sourceCommit"] != commit:
        raise ValueError("digest lock belongs to a different source commit")
    return lock["images"]


def rewrite_images(source, digests):
    validate_digests(digests)

    def replace(match):
        name = match.group("name")
        if name not in digests:
            raise ValueError(f"unrecognized deployed Promise image: {name}")
        return match.group("prefix") + "@" + digests[name]

    return IMAGE_PATTERN.sub(replace, source)


def fetch_digests(commit):
    digests = {}
    for name in IMAGES:
        result = subprocess.run(
            ["az", "acr", "repository", "show", "--name", "teknologieur1acr",
             "--image", f"kratix-{name}:{commit}", "--query", "digest", "-o", "tsv",
             "--subscription", "Teknologi"],
            check=True, capture_output=True, text=True,
        )
        digests[name] = result.stdout.strip()
    validate_digests(digests)
    return digests


def pin(root, commit, digests):
    if not COMMIT_PATTERN.fullmatch(commit):
        raise ValueError("commit must be a full 40-character Git SHA")
    validate_digests(digests)
    rewritten = {}
    found = set()
    for relative_path in MANIFESTS:
        path = root / relative_path
        source = path.read_text()
        found.update(match.group("name") for match in IMAGE_PATTERN.finditer(source))
        rewritten[path] = rewrite_images(source, digests)
    if found != set(IMAGES):
        raise ValueError(f"manifest image set differs from expected: {sorted(found)}")
    for path, content in rewritten.items():
        path.write_text(content)
    lock_path = root / "release" / "promise-images.json"
    lock_path.parent.mkdir(exist_ok=True)
    lock_path.write_text(json.dumps({"sourceCommit": commit, "images": digests}, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--digest-file", type=pathlib.Path,
                        help="read an already verified release lock instead of querying ACR")
    args = parser.parse_args()
    digests = digests_from_lock(args.digest_file, args.commit) if args.digest_file else fetch_digests(args.commit)
    pin(ROOT, args.commit, digests)
    print(f"Pinned {len(IMAGES)} Promise images from {args.commit}")


if __name__ == "__main__":
    main()
