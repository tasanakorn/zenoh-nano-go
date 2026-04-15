import subprocess


def test_go_scout_finds_router(go_bins):
    """Go scout discovers the live zenohd over multicast."""
    result = subprocess.run(
        [go_bins["z_scout"], "-a", "224.0.0.224:31746", "-t", "3s"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=8,
    )

    output = result.stdout.decode()
    assert result.returncode == 0, f"Non-zero exit: {result.returncode}, stderr: {result.stderr.decode()}"
    assert "router" in output or "hello(s) received" in output, f"Unexpected output: {output!r}"
