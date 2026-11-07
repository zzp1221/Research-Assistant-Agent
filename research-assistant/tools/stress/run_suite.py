#!/usr/bin/env python3
"""Memory stress suite runner.

Usage (inside the container):
    python3 run_suite.py --suite MB --run-id stress-... --out /tmp/stress/<run>
    python3 run_suite.py --suite all --run-id ... --out ...

Each suite starts an isolated backend + deterministic mock, executes the cases
and writes verdict.json per case plus a suite scorecard. Suites that are not
yet implemented report their cases as not_executed (never as pass).
"""
import argparse
import importlib.util
import json
import os
import sys
import time
import traceback

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import common  # noqa: E402

SUITES = ["MB", "LC", "SC", "LT", "LP", "CP", "PF", "RB"]


def load_cases(path=None):
    path = path or os.path.join(HERE, "cases.json")
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def load_module(suite):
    path = os.path.join(HERE, "suites", "case_%s.py" % suite.lower())
    if not os.path.exists(path):
        return None
    spec = importlib.util.spec_from_file_location("case_%s" % suite.lower(), path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def placeholder_cases(suite, cases, run_id, evidence_root, reason):
    out = []
    for item in cases:
        if item["suite"] != suite:
            continue
        case = common.Case(item["case_id"], suite, item["priority"], item["title"], run_id, evidence_root)
        case.blocked(reason)
        out.append(case.finish())
    return out


def write_scorecard(suite, verdicts, run_id, out_dir, extra=None):
    os.makedirs(out_dir, exist_ok=True)
    counts = {}
    for verdict in verdicts:
        counts[verdict["status"]] = counts.get(verdict["status"], 0) + 1
    p0 = [v for v in verdicts if v["priority"] == "P0"]
    p0_pass = [v for v in p0 if v["status"] == "pass"]
    lines = ["# %s 套件计分卡" % suite, "",
             "- Run: %s" % run_id,
             "- Cases: %d" % len(verdicts),
             "- P0: %d/%d pass" % (len(p0_pass), len(p0)),
             "- pass: %d, fail: %d, blocked: %d, not_executed: %d" % (
                 counts.get("pass", 0), counts.get("fail", 0), counts.get("blocked", 0), counts.get("not_executed", 0)), ""]
    lines.append("| 用例 | 优先级 | 结果 | 错误类型 | 备注 |")
    lines.append("|---|---|---|---|---|")
    for verdict in verdicts:
        note = ""
        if verdict.get("notes"):
            note = verdict["notes"].split("\n")[0][:80]
        lines.append("| %s | %s | %s | %s | %s |" % (
            verdict["case_id"], verdict["priority"], verdict["status"].upper(),
            ",".join(verdict.get("error_types") or []), note))
    if extra:
        lines.append("")
        lines.append("## 指标")
        lines.append("")
        lines.append("~~~json")
        lines.append(json.dumps(extra, ensure_ascii=False, indent=2))
        lines.append("~~~")
    with open(os.path.join(out_dir, "SCORECARD.md"), "w", encoding="utf-8") as handle:
        handle.write("\n".join(lines) + "\n")
    with open(os.path.join(out_dir, "scorecard.json"), "w", encoding="utf-8") as handle:
        json.dump({"suite": suite, "run_id": run_id, "counts": counts,
                   "p0_total": len(p0), "p0_pass": len(p0_pass), "verdicts": verdicts, "extra": extra},
                  handle, ensure_ascii=False, indent=2)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", default="all", choices=["all"] + SUITES)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--cases", default=None)
    parser.add_argument("--only", default="")
    args = parser.parse_args()
    only = {item.strip().upper() for item in args.only.split(",") if item.strip()}
    suites = SUITES if args.suite == "all" else [args.suite]
    cases = load_cases(args.cases)
    summary = {}
    started = time.time()
    for suite in suites:
        suite_cases = [c for c in cases if c["suite"] == suite and (not only or c["case_id"].upper() in only)]
        evidence_root = os.path.join(args.out, "evidence", args.run_id)
        module = load_module(suite)
        suite_dir = os.path.join(args.out, "scorecards")
        print("[%s] %d cases" % (suite, len(suite_cases)), flush=True)
        if module is None:
            verdicts = placeholder_cases(suite, cases, args.run_id, evidence_root,
                                         "suite implementation pending")
            write_scorecard(suite, verdicts, args.run_id, suite_dir)
            summary[suite] = {"counts": {"not_executed": len(verdicts)}}
            continue
        try:
            verdicts, extra = module.run_suite(suite_cases, args.run_id, evidence_root, common, args.out)
        except Exception as exc:
            traceback.print_exc()
            verdicts = []
            for item in suite_cases:
                case = common.Case(item["case_id"], suite, item["priority"], item["title"], args.run_id, evidence_root)
                case.blocked("suite crashed: %s" % exc)
                verdicts.append(case.finish())
            extra = {"suite_error": str(exc)}
        write_scorecard(suite, verdicts, args.run_id, suite_dir, extra)
        counts = {}
        for verdict in verdicts:
            counts[verdict["status"]] = counts.get(verdict["status"], 0) + 1
        summary[suite] = {"counts": counts, "extra": extra}
        print("[%s] done %s" % (suite, counts), flush=True)
    summary["elapsed_seconds"] = round(time.time() - started, 1)
    common.write_json(os.path.join(args.out, "summary.json"), summary)
    print(json.dumps(summary, ensure_ascii=False))


if __name__ == "__main__":
    main()
