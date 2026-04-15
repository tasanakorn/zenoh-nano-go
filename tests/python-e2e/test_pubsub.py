import json
import subprocess
import threading
import time

import zenoh


def make_session(router):
    conf = zenoh.Config()
    conf.insert_json5("connect/endpoints", json.dumps([router]))
    return zenoh.open(conf)


def test_go_publish_python_subscribe(router, go_bins):
    """Go publishes → Python subscriber receives."""
    received = []
    event = threading.Event()

    session = make_session(router)

    def on_sample(sample):
        received.append(sample)
        event.set()

    sub = session.declare_subscriber("test/go2py", on_sample)
    time.sleep(0.3)  # let declare propagate

    subprocess.run(
        [go_bins["z_put"], "-e", router, "-k", "test/go2py", "-v", "hello-from-go"],
        check=True,
    )

    arrived = event.wait(timeout=5)
    sub.undeclare()
    session.close()

    assert arrived, "No sample received within 5s"
    assert received[0].payload.to_bytes() == b"hello-from-go"


def test_python_publish_go_subscribe(router, go_bins):
    """Python publishes → Go subscriber receives."""
    proc = subprocess.Popen(
        [go_bins["z_sub"], "-e", router, "-k", "test/py2go"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    time.sleep(1)  # let Go session open + subscriber declare

    session = make_session(router)
    session.put("test/py2go", b"hello-from-python")
    session.close()

    time.sleep(0.5)  # let delivery complete

    proc.terminate()
    try:
        stdout, _ = proc.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        stdout, _ = proc.communicate()

    output = stdout.decode()
    assert "test/py2go" in output, f"Key not in Go output: {output!r}"
    assert "hello-from-python" in output, f"Value not in Go output: {output!r}"
