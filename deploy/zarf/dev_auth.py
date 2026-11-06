#!/usr/bin/env python3
"""Create fresh local development credentials without files or log output."""
import base64
import hashlib
import hmac
import json
import secrets
import subprocess
import time


def apply(document):
    subprocess.run(["uds", "zarf", "tools", "kubectl", "apply", "-f", "-"],
                   input=json.dumps(document), text=True, check=True,
                   stdout=subprocess.DEVNULL)


def encoded(value):
    return base64.urlsafe_b64encode(value).rstrip(b"=")


def main():
    apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": "ghost-seeker"}})
    operator = secrets.token_hex(32)
    signing = secrets.token_hex(32)
    header = encoded(b'{"alg":"HS256","typ":"JWT"}')
    claims = encoded(json.dumps({"sub": "edge-1", "role": "unit",
                                "exp": int(time.time()) + 720 * 3600}).encode())
    payload = header + b"." + claims
    token = (payload + b"." + encoded(hmac.new(signing.encode(), payload, hashlib.sha256).digest())).decode()
    for name, data in {
        "shore-auth": {"signing-key": signing, "operator-token": operator},
        "operator-auth": {"operator-token": operator},
        "edge-auth": {"unit-token": token, "operator-token": operator},
    }.items():
        apply({"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
               "metadata": {"name": name, "namespace": "ghost-seeker"}, "stringData": data})


if __name__ == "__main__":
    main()
