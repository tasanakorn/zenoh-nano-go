"""
Zenoh router helper for z_latency benchmark.

Starts a Python zenoh router that listens on TCP and responds to multicast
scout messages. Prints "READY" once the session is open, then blocks until
stdin is closed (parent process exits or closes the pipe).
"""
import sys
import zenoh

LISTEN_PORT = 17447
SCOUT_ADDR  = "224.0.0.224:7446"

conf = zenoh.Config.from_json5(f"""
{{
  "mode": "router",
  "listen": {{
    "endpoints": ["tcp/127.0.0.1:{LISTEN_PORT}"]
  }},
  "scouting": {{
    "multicast": {{
      "enabled": true,
      "address": "{SCOUT_ADDR}"
    }}
  }}
}}
""")

session = zenoh.open(conf)
print("READY", flush=True)

sys.stdin.read()
session.close()
