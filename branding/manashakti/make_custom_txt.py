#!/usr/bin/env python3
"""Sign a custom-client config into the custom.txt format read by
`read_custom_client` (src/common.rs): base64(ed25519 signature || JSON).

    make_custom_txt.py keygen <private-key-file>        # once; prints public key
    make_custom_txt.py sign <config.json> <private-key-file> > custom.txt
    make_custom_txt.py verify <custom.txt> <public-key-base64>

The public key printed by `keygen` must match CUSTOM_CLIENT_PUBLIC_KEY in src/common.rs.
Keep the private key out of the repository.
"""
import base64
import json
import sys

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import (
    Ed25519PrivateKey,
    Ed25519PublicKey,
)


def load_private(path):
    raw = base64.b64decode(open(path).read().strip())
    return Ed25519PrivateKey.from_private_bytes(raw)


def public_b64(key):
    raw = key.public_key().public_bytes(
        serialization.Encoding.Raw, serialization.PublicFormat.Raw
    )
    return base64.b64encode(raw).decode()


def keygen(path):
    key = Ed25519PrivateKey.generate()
    raw = key.private_bytes(
        serialization.Encoding.Raw,
        serialization.PrivateFormat.Raw,
        serialization.NoEncryption(),
    )
    with open(path, "x") as f:  # never overwrite an existing key
        f.write(base64.b64encode(raw).decode() + "\n")
    print(public_b64(key))


def sign(config_path, key_path):
    config = json.load(open(config_path))
    if "app-name" not in config:
        sys.exit("config must contain app-name")
    msg = json.dumps(config, separators=(",", ":")).encode()
    key = load_private(key_path)
    # libsodium crypto_sign "combined" format: 64-byte signature followed by the message
    print(base64.b64encode(key.sign(msg) + msg).decode())


def verify(custom_path, pub_b64):
    blob = base64.b64decode(open(custom_path).read().strip())
    pub = Ed25519PublicKey.from_public_bytes(base64.b64decode(pub_b64))
    pub.verify(blob[:64], blob[64:])  # raises InvalidSignature on failure
    print(json.dumps(json.loads(blob[64:]), indent=2))


if __name__ == "__main__":
    cmds = {"keygen": (keygen, 1), "sign": (sign, 2), "verify": (verify, 2)}
    if len(sys.argv) < 2 or sys.argv[1] not in cmds or len(sys.argv) != cmds[sys.argv[1]][1] + 2:
        sys.exit(__doc__)
    fn, _ = cmds[sys.argv[1]]
    fn(*sys.argv[2:])
