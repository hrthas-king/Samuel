"""Run both packaged binaries and exercise their JSON command interface."""

from datetime import datetime, timedelta, timezone
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
    architectures = subprocess.check_output(["lipo", "-archs", binary], text=True).split()
    assert set(architectures) == {"arm64", "x86_64"}, (binary, architectures)
    result = subprocess.run([binary, "--version"], check=True, capture_output=True, text=True)
    assert result.stdout.strip() == f"{binary.name} {version}", result.stdout

with TemporaryDirectory() as tmp:
    root = Path(__file__).resolve().parents[1]
    geometry = Path(tmp) / "ChartWindowTests"
    subprocess.run([
        "xcrun", "swiftc", "-parse-as-library", root / "macos/ChartWindow.swift",
        root / "tests/macos/ChartWindowTests.swift", "-o", geometry,
    ], check=True)
    subprocess.run([geometry], check=True)
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
    chart = chart_frame["chart"]
    beijing = timezone(timedelta(hours=8))
    start, end = (datetime.fromtimestamp(chart["window"][key], beijing) for key in ("start", "end"))
    assert start.strftime("%H:%M:%S") == "10:00:00" and end.strftime("%H:%M:%S") == "09:00:00"
    assert end - start == timedelta(hours=23)
    stamps = [point["Time"] for point in chart["points"]]
    assert stamps == sorted(set(stamps)) and all(start.timestamp() <= t <= end.timestamp() for t in stamps)
    assert chart["breaks"] == [i for i in range(1, len(stamps)) if stamps[i] - stamps[i-1] > 15 * 60]
    assert json.loads(config.read_text())["market_group"] == "soxx_smh"
    subprocess.run([app, "--check-feed"], input=json.dumps(chart_frame) + "\n", text=True, check=True)
    # Ordinary quote mode must not initialize Sentinel or its storage.
    sentinel_dir = config.parent / "us_market_sentinel"
    assert not sentinel_dir.exists()
    offline = {**os.environ, "SAMUEL_CONFIG": str(config), "HTTPS_PROXY": "http://127.0.0.1:1",
               "NO_PROXY": "", "no_proxy": "", "FRED_API_KEY": "", "OPENAI_API_KEY": "",
               "SAMUEL_LLM_API_KEY": "", "SAMUEL_LLM_MODEL": ""}
    result = subprocess.run([helper, "sentinel", "run", "--style", "right_confirm", "--json"],
                            env=offline, capture_output=True, check=True, timeout=30)
    report = json.loads(result.stdout)
    assert report["quant_base_score"] == 50 and report["coverage_ratio"] == 0
    assert report["strategy_style"] == "right_confirm" and report["analysis_mode"] == "quant_only"
    assert report["fear_indices"]["vix"]["level"] is None
    assert report["fear_indices"]["cnn_fear_greed"]["level"] is None
    assert report["risk_data_throttle_applied"] and report["credit_data_missing"]
    assert report["suggested_dca_multiplier"] <= 1 and "暂停倍投" in report["executive_summary"]
    assert report["key_drivers"] == [] and report["headlines_48h"] == []
    assert len([w for w in report["missing_data_warnings"] if "FRED_API_KEY" in w]) == 1
    subprocess.run([app, "--check-sentinel"], input=result.stdout, check=True)
    # Old snapshots have no fear_indices. Also exercise the native decoder with
    # both real-valued fields and a genuine zero on the CNN scale.
    del report["fear_indices"]
    extra = {k: report.pop(k) for k in ("risk_data_throttle_applied", "credit_data_missing", "risk_data_warning", "headlines_48h")}
    def native_accepts():
        return subprocess.run([app, "--check-sentinel"], input=json.dumps(report).encode()).returncode == 0
    assert native_accepts()
    report.update(extra)
    report["headlines_48h"] = [{"title": "Published headline", "source": "Federal Reserve", "url": "https://www.federalreserve.gov/newsevents/pressreleases/test.htm", "published_at": "2025-03-07T21:00:00Z"}]
    assert native_accepts()
    del report["headlines_48h"][0]["url"]
    assert native_accepts()
    report["headlines_48h"][0]["source"] = 7
    assert not native_accepts()
    report["headlines_48h"] = []
    report["fear_indices"] = {"vix": {"level": 20.5, "percentile_1y": 73.2, "date": "2025-03-07"},
                             "cnn_fear_greed": {"level": 0, "rating": "extreme fear", "rating_zh": "极度恐惧",
                                                "published_at": "2025-03-07T21:00:00Z"}}
    assert native_accepts()
    report["fear_indices"]["vix"]["percentile_1y"] = "malformed"
    assert not native_accepts()
    assert len(list((sentinel_dir / "snapshots").glob("*.json"))) == 1
    latest = subprocess.check_output([helper, "sentinel", "latest", "--style", "right_confirm", "--json"], env=offline)
    assert json.loads(latest)["strategy_style"] == "right_confirm"
    empty_latest = subprocess.check_output([helper, "sentinel", "latest", "--style", "left_dca", "--json"], env=offline)
    assert empty_latest.strip() == b""
    summary = subprocess.check_output([helper, "sentinel", "calibrate", "--json"], env=offline)
    assert json.loads(summary)["groups"][0]["spx"]["20d"]["samples"] == 0
    saved = subprocess.run([helper, "sentinel", "settings", "set"], input=b'{"backend":"opencode","opencodeModel":"opencode/deepseek-v4.1-flash-free","agyModel":"gemini-3.8-flash-high","fredKey":"fixture-only"}',
                           env=offline, capture_output=True, check=True)
    settings = json.loads(subprocess.check_output([helper, "sentinel", "settings", "get"], env=offline))
    assert settings["backend"] == "opencode" and settings["opencodeModel"] == "opencode/deepseek-v4.1-flash-free" and settings["agyModel"] == "gemini-3.8-flash-high" and settings["hasFREDKey"] and b"fixture-only" not in saved.stdout
