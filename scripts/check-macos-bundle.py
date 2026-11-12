"""Run both packaged binaries and exercise their JSON command interface."""

import json
import os
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory


bundle = Path(sys.argv[1]).resolve()
version = sys.argv[2]
helper = bundle / "Contents/Helpers/SamuelData"
app = bundle / "Contents/MacOS/Samuel"
for binary in (app, helper):
    subprocess.run(["lipo", "-verify_arch", "arm64", "x86_64", binary], check=True)
    result = subprocess.run([binary, "--version"], check=True, capture_output=True, text=True)
    assert result.stdout.strip() == f"{binary.name} {version}", result.stdout

with TemporaryDirectory() as tmp:
    config = Path(tmp) / "config.json"
    commands = [
        {"action": "language", "value": "en"},
        {"action": "interval", "number": 5},
        {"action": "network", "on": True},
        {"action": "group", "value": "soxx_smh"},
        {"action": "open", "value": "SOXX"},
        {"action": "close"},
    ]
    result = subprocess.run(
        [helper], input="".join(json.dumps(command) + "\n" for command in commands),
        env={**os.environ, "SAMUEL_CONFIG": str(config)},
        check=True, capture_output=True, text=True, timeout=20,
    )
    frames = [json.loads(line) for line in result.stdout.splitlines()]
    assert frames[0]["group"] == "voo_qqq" and "chart" not in frames[0]
    assert frames[-1]["group"] == "soxx_smh" and "chart" not in frames[-1]
    assert frames[-1]["language"] == "en" and frames[-1]["showNet"] and frames[-1]["interval"] == 5
    chart_frame = next(frame for frame in frames if frame.get("chart", {}).get("symbol") == "SOXX")
    assert json.loads(config.read_text())["market_group"] == "soxx_smh"
    subprocess.run([app, "--check-feed"], input=json.dumps(chart_frame) + "\n", text=True, check=True)
