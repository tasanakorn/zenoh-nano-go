import os
import subprocess
import tempfile

import pytest

ROUTER = "tcp/172.26.2.155:31747"
GO_ROOT = "/home/tas/Documents/Projects/workspace-stele/zenoh-nano-go"


@pytest.fixture(scope="session")
def router():
    return ROUTER


@pytest.fixture(scope="session")
def go_root():
    return GO_ROOT


@pytest.fixture(scope="session")
def go_bins(tmp_path_factory):
    """Build Go example binaries once per session."""
    out = tmp_path_factory.mktemp("bins")
    bins = {}
    for name in ("z_put", "z_sub", "z_scout", "z_queryable"):
        dst = str(out / name)
        subprocess.run(
            ["go", "build", "-o", dst, f"./examples/{name}"],
            cwd=GO_ROOT,
            check=True,
        )
        bins[name] = dst
    return bins
