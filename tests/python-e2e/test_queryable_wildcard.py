"""Wildcard queryable test: Go z_queryable on demo/** replies to concrete keys."""
import json
import subprocess
import time

import zenoh


def make_session(router):
    conf = zenoh.Config()
    conf.insert_json5("connect/endpoints", json.dumps([router]))
    return zenoh.open(conf)


def test_go_queryable_wildcard(router, go_bins):
    """Queryable declared on demo/** answers get() on several concrete keys."""
    wildcard = "demo/**"
    concrete_keys = [
        "demo/alpha",
        "demo/beta/one",
        "demo/gamma/deep/key",
    ]

    proc = subprocess.Popen(
        [go_bins["z_queryable"], "-e", router, "-k", wildcard],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    time.sleep(1)  # let Go session open + queryable declare

    try:
        session = make_session(router)
        for key in concrete_keys:
            handler = session.get(key, timeout=5.0)
            replies = list(handler)
            assert len(replies) == 1, (
                f"Expected 1 reply for key '{key}', got {len(replies)}"
            )
            reply = replies[0].ok
            assert reply is not None, f"Reply for '{key}' was an error"
            expected = b"Queryable reply: " + key.encode()
            assert reply.payload.to_bytes() == expected, (
                f"Unexpected payload for '{key}': {reply.payload.to_bytes()!r}"
            )
        session.close()
    finally:
        proc.terminate()
        try:
            proc.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.communicate()
