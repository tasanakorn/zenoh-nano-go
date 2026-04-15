"""Basic queryable test: Go z_queryable serves one reply per query."""
import json
import subprocess
import time

import zenoh


def make_session(router):
    conf = zenoh.Config()
    conf.insert_json5("connect/endpoints", json.dumps([router]))
    return zenoh.open(conf)


def test_go_queryable_python_get(router, go_bins):
    """Python get() → Go queryable replies with correct payload."""
    key = "demo/example/queryable"

    proc = subprocess.Popen(
        [go_bins["z_queryable"], "-e", router, "-k", key],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    time.sleep(1)  # let Go session open + queryable declare

    try:
        session = make_session(router)
        handler = session.get(key, timeout=5.0)
        replies = list(handler)
        session.close()
    finally:
        proc.terminate()
        try:
            proc.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.communicate()

    assert len(replies) == 1, f"Expected 1 reply, got {len(replies)}"
    reply = replies[0].ok
    assert reply is not None, "Reply was an error"
    assert reply.payload.to_bytes() == b"Queryable reply: " + key.encode(), (
        f"Unexpected payload: {reply.payload.to_bytes()!r}"
    )
