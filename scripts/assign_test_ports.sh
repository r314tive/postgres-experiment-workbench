#!/usr/bin/env bash
set -euo pipefail

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to assign dynamic test ports" >&2
  exit 2
fi

python3 - <<'PY'
import socket

names = [
    "POSTGRES_PORT",
    "POSTGRES_REPLICA_PORT",
    "POSTGRES_LOGICAL_SUBSCRIBER_PORT",
    "PGBOUNCER_PORT",
    "POSTGRES_UPGRADE_OLD_PORT",
    "POSTGRES_UPGRADE_NEW_PORT",
]

sockets = []
try:
    for name in names:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.bind(("127.0.0.1", 0))
        sockets.append((name, sock))

    for name, sock in sockets:
        print(f"{name}={sock.getsockname()[1]}")
finally:
    for _, sock in sockets:
        sock.close()
PY
