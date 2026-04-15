"""Many-replies test: a Python queryable sends N replies; a Python get() receives all N."""
import json
import time

import zenoh

N_REPLIES = 5


def make_session(router):
    conf = zenoh.Config()
    conf.insert_json5("connect/endpoints", json.dumps([router]))
    return zenoh.open(conf)


def test_queryable_many_replies(router):
    """Python queryable sends N replies; get() collects all N."""
    key = "test/many-replies"

    server = make_session(router)

    def on_query(query):
        for i in range(N_REPLIES):
            reply_key = f"{key}/{i}"
            payload = f"reply-{i}".encode()
            query.reply(reply_key, payload)

    qa = server.declare_queryable(key, on_query)
    time.sleep(0.3)  # let declare propagate

    client = make_session(router)
    handler = client.get(key, timeout=5.0)
    replies = list(handler)
    client.close()

    qa.undeclare()
    server.close()

    assert len(replies) == N_REPLIES, (
        f"Expected {N_REPLIES} replies, got {len(replies)}"
    )
    payloads = {r.ok.payload.to_bytes() for r in replies if r.ok is not None}
    expected = {f"reply-{i}".encode() for i in range(N_REPLIES)}
    assert payloads == expected, f"Payload mismatch: {payloads!r} != {expected!r}"
