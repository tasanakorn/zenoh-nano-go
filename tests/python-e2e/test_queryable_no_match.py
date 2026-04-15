"""No-match test: get() on an unregistered key returns zero replies without hanging."""
import json
import subprocess
import time

import zenoh


def make_session(router):
    conf = zenoh.Config()
    conf.insert_json5("connect/endpoints", json.dumps([router]))
    return zenoh.open(conf)


def test_go_queryable_no_match(router, go_bins):
    """get() on a key not covered by any queryable completes with zero replies."""
    registered_key = "demo/example/queryable"
    unmatched_key = "demo/other/key"

    proc = subprocess.Popen(
        [go_bins["z_queryable"], "-e", router, "-k", registered_key],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    time.sleep(1)  # let Go session open + queryable declare

    try:
        session = make_session(router)
        # Short timeout: there is no queryable for this key, must not hang.
        handler = session.get(unmatched_key, timeout=3.0)
        replies = list(handler)
        session.close()
    finally:
        proc.terminate()
        try:
            proc.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.communicate()

    assert len(replies) == 0, (
        f"Expected 0 replies for unmatched key, got {len(replies)}: {replies}"
    )
