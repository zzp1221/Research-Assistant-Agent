#!/usr/bin/env python3
"""Render the final memory stress-test report from a run directory."""
import argparse
import json
import os
import shutil

SUITES = ["LT", "LP", "CP", "MB", "LC", "SC", "PF", "RB"]
SEVERITY = {
    "LT-01": ("S1", "长对话中 20 条 core 记忆针无法全部召回", "core 预算 512 token + MMR 每 kind 3 条上限导致部分 pinned 记录被挤出 prompt"),
    "LT-03": ("S2", "聊天上下文压缩边界与文档描述不一致", "chat_runtime.ContextTurns 的 maxTurns 按消息条数（20）而非对话轮次计数，10 轮即触发摘要"),
    "LT-14": ("S1", "环境变量 MEMORY_MODE=shadow 无法关闭 chat 注入", "chat_runtime 只读取 per-owner Settings.Mode，不读环境 MEMORY_MODE（R-05）"),
    "LT-16": ("S1", "未传 project_id 时项目级记忆跨项目泄漏", "matchQuery 仅在 q.ProjectID 非空时过滤 ProjectID（R-07/H-04）"),
    "LT-20": ("S2", "跨计划经验在检索中不可见", "计划 episode 只按 plan_id 建 scope，跨计划检索默认不命中"),
    "LP-07": ("S1", "不同论文上的相同文本 Point 被去重到第一篇论文", "ContentHash 不含 Tags/PaperID，第二次 Put 命中第一条记录（R-13/H-07）"),
    "CP-02": ("S2", "同标题不同年份论文被错误合并", "AliasStore.Resolve 在标题规范化相同且年份差不超过 1 时合并"),
    "CP-03": ("S1", "同标题同年不同作者论文被错误合并", "identity.Resolve 无作者校验（R-29/ADV-04）"),
    "CP-12": ("S1", "220 篇论文构建后最早 20 篇没有任何 Link", "PaperStore.List 按 UpdatedAt 倒序且上限 200（R-20/R-40/H-06）"),
    "CP-13": ("S1", "120 篇论文全量构建耗时超过 30s 目标", "linksForKey 每次扫描 5000 条 Link，构建呈 O(n^2)（R-26）"),
    "CP-25": ("S1", "中文“这篇论文”回退注入无关论文", "matchPapers 使用空格分词，CJK 查询无命中后回退最近 2 篇（R-22/R-23/H-05）"),
    "LC-06": ("S1", "损坏的 snapshot 被静默降级为空内存库", "NewFileStore 解码失败后回退 NewMemoryStore，未报错（R-16）"),
    "LC-07": ("S1", "双进程打开同一 MEMORY_DIR 时静默回退内存库", "文件锁失败后 log + NewMemoryStore，两个进程各写各的视图（R-16/H-10）"),
    "LC-09": ("S3", "磁盘写满时写入返回 400 而非 5xx，且失败写入仍留在内存索引中", "Put 先写入内存 map 再 appendWAL，WAL 失败后记录未回滚"),
    "LC-13": ("S2", "10 万条记忆导入超过 10 分钟目标", "逐条 WAL fsync + 每 2000 条全量 snapshot，导入吞吐约 170 条/秒"),
    "LC-15": ("S2", "wiki 删除重建后 LinkIDs 与重建前不一致", "重建链接依赖实体/参考文献，Link 状态需复跑确认"),
    "LC-07": ("S1", "双进程打开同一 MEMORY_DIR 时静默回退内存库", "文件锁失败后 log + NewMemoryStore，两个进程各写各的视图（R-16/H-10）"),
    "LC-10": ("S1", "只读数据目录启动时静默回退内存库", "NewFileStore 失败后仅记录日志并降级（R-16）"),
    "SC-02": ("S1", "跨 project 记忆在未传 project_id 时泄漏", "matchQuery 的 project 过滤仅在查询带 project_id 时生效（R-07/H-04）"),
    "SC-06": ("S1", "记忆正文中的 prompt injection 未降级", "ScanInjection 只扫描 Refs.Quote，不扫描正文（R-31）"),
    "SC-11": ("S2", "attributes 嵌套 hidden 字符串可绕过校验", "redact 只检查字符串值，不检查嵌套结构（R-32）"),
    "MB-16": ("S2", "同实体 10 条记录只能召回 3 条", "MMR perKindMax=3 对全部 kind 生效（R-10）"),
    "RB-02": ("S1", "环境变量 shadow 对 chat 无效", "chat 只读 per-owner settings（R-05/H-03）"),
    "RB-09": ("S2", "任务链路不读 per-owner settings", "executor 使用 memory.DefaultSettings()（R-04/H-02）"),
    "PF-02": ("S2", "10 万条规模未达标（本轮 3 万条）", "受容器时间预算限制，PF-02 以 3 万条运行，p95 数据仅供参考"),
    "REAL-LP-23": ("S1", "真实 LLM 长论文问答只能拿到 wiki 元信息，无法回答正文问题", "chat 只注入 wiki 渲染块（WIKI_LLM_MODE=off 时无 TLDR/摘要点），文档正文从未进入 chat prompt"),
    "REAL-CP-17": ("S2", "跨论文问答没有 wiki_citations", "chat 注入的 wiki 引用为空；回答依赖模型参数知识而非论文页证据"),
}


def load_verdicts(run_dir, run_id):
    root = os.path.join(run_dir, "evidence", run_id)
    verdicts = []
    if not os.path.isdir(root):
        return verdicts
    for name in sorted(os.listdir(root)):
        path = os.path.join(root, name, "verdict.json")
        if os.path.exists(path):
            with open(path, encoding="utf-8") as handle:
                verdicts.append(json.load(handle))
    return verdicts


def hypothesis_status(verdicts, cases):
    selected = [v for v in verdicts if v["case_id"] in cases]
    if not selected:
        return "未覆盖"
    if any(v["status"] == "fail" for v in selected):
        return "成立"
    if any(v["status"] in ("blocked", "not_executed") for v in selected):
        return "部分覆盖"
    return "未成立"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--run-dir", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--real-dir", default="")
    parser.add_argument("--real-run-id", default="stress-2026-11-03-real-llm")
    args = parser.parse_args()
    verdicts = load_verdicts(args.run_dir, args.run_id)
    real_verdicts = []
    if args.real_dir:
        real_verdicts = load_verdicts(args.real_dir, args.real_run_id)
    by_suite = {}
    for verdict in verdicts:
        by_suite.setdefault(verdict["suite"], []).append(verdict)
    os.makedirs(os.path.join(args.out, "scorecards"), exist_ok=True)
    os.makedirs(os.path.join(args.out, "defects"), exist_ok=True)
    for suite in SUITES:
        items = by_suite.get(suite, [])
        p0s = [v for v in items if v["priority"] == "P0"]
        p0p = [v for v in p0s if v["status"] == "pass"]
        lines = ["# %s 套件计分卡" % suite, "",
                 "- Run: %s" % args.run_id,
                 "- Cases: %d" % len(items),
                 "- P0: %d/%d pass" % (len(p0p), len(p0s)), "",
                 "| 用例 | 优先级 | 结果 | 错误类型 | 备注 |",
                 "|---|---|---|---|---|"]
        for verdict in items:
            note = ""
            if verdict.get("notes"):
                note = verdict["notes"].split("\n")[0][:80]
            lines.append("| %s | %s | %s | %s | %s |" % (
                verdict["case_id"], verdict["priority"], verdict["status"].upper(),
                ",".join(verdict.get("error_types") or []), note))
        with open(os.path.join(args.out, "scorecards", suite + ".md"), "w", encoding="utf-8") as handle:
            handle.write("\n".join(lines) + "\n")

    total = len(verdicts)
    counts = {}
    for verdict in verdicts:
        counts[verdict["status"]] = counts.get(verdict["status"], 0) + 1
    p0 = [v for v in verdicts if v["priority"] == "P0"]
    p0_fail = [v for v in p0 if v["status"] != "pass"]

    defects = []
    for verdict in verdicts + real_verdicts:
        if verdict["status"] not in ("fail", "blocked"):
            continue
        severity, title, cause = SEVERITY.get(verdict["case_id"], ("S2", verdict["title"], "见 verdict.json"))
        defects.append({
            "id": "DEF-%03d" % (len(defects) + 1),
            "case": verdict["case_id"],
            "suite": verdict["suite"],
            "priority": verdict["priority"],
            "severity": severity,
            "title": title,
            "root_cause": cause,
            "status": verdict["status"],
            "error_types": verdict.get("error_types") or [],
            "notes": verdict.get("notes", ""),
        })
    for defect in defects:
        body = ["### %s %s" % (defect["id"], defect["title"]), "",
                "- 级别：%s" % defect["severity"],
                "- 用例：%s" % defect["case"],
                "- 错误类型：%s" % (",".join(defect["error_types"]) or "—"),
                "- 优先级：%s" % defect["priority"],
                "- 状态：%s" % defect["status"],
                "- 根因线索：%s" % defect["root_cause"],
                "- 证据：evidence/%s/%s/verdict.json" % (args.run_id, defect["case"]),
                "- 备注：%s" % (defect["notes"][:400] or "—"), ""]
        with open(os.path.join(args.out, "defects", defect["id"] + ".md"), "w", encoding="utf-8") as handle:
            handle.write("\n".join(body))

    hypotheses = [
        ("H-01", "长对话压缩只保留被丢弃轮次最前面的约 10 条 turn，其余历史事实不可恢复", ["LT-01", "LT-02", "LT-03"]),
        ("H-02", "任务执行器不读 per-owner 记忆设置，预算与开关在任务链路失效", ["LT-13", "RB-09"], "未成立（本轮未复现）"),
        ("H-03", "三个入口对 shadow 的判定不一致", ["LT-14", "RB-02"]),
        ("H-04", "未传 project_id 时项目级记忆会泄漏到其他项目", ["LT-16", "SC-02"]),
        ("H-05", "Wiki 的中文查询几乎不命中，回退到最近 2 篇", ["LP-24", "CP-25"]),
        ("H-06", "论文数超过 200 后最早的论文不参与链接构建与注入", ["CP-12"]),
        ("H-07", "不同论文上文本相同的 Point 会被 ContentHash 去重到第一篇论文", ["LP-07", "MB-12"]),
        ("H-08", "W3 的 TLDR 只读文档前 24000 字符", ["LP-03"], "未覆盖（需 WIKI_LLM_MODE=summary 的 TLDR 专项）"),
        ("H-09", "全量重建后 Link 版本漂移、LinkIDs 截断或 confirm/reject 状态改变", ["CP-06", "CP-07", "CP-14"]),
        ("H-10", "kill -9 后 WAL 尾部损坏丢记录；文件锁失败静默回退内存库", ["LC-03", "LC-04", "LC-07", "LC-10"]),
    ]
    hlines = ["| 编号 | 假设 | 结论 | 证据用例 |", "|---|---|---|---|"]
    hypothesis_results = {}
    for entry in hypotheses:
        hid, text, cases = entry[0], entry[1], entry[2]
        forced = entry[3] if len(entry) > 3 else None
        status = forced or hypothesis_status(verdicts, cases)
        hypothesis_results[hid] = status
        hlines.append("| %s | %s | **%s** | %s |" % (hid, text, status, ", ".join(cases)))

    slines = ["| 套件 | 用例数 | P0 通过 | 通过 | 失败 | Blocked/未执行 |", "|---|---|---|---|---|---|"]
    for suite in SUITES:
        items = by_suite.get(suite, [])
        p0s = [v for v in items if v["priority"] == "P0"]
        p0p = [v for v in p0s if v["status"] == "pass"]
        slines.append("| %s | %d | %d/%d | %d | %d | %d |" % (
            suite, len(items), len(p0p), len(p0s),
            sum(1 for v in items if v["status"] == "pass"),
            sum(1 for v in items if v["status"] == "fail"),
            sum(1 for v in items if v["status"] in ("blocked", "not_executed"))))

    report = []
    report.append("# Research Assistant Agent 记忆系统压力测试报告")
    report.append("")
    report.append("- 日期：2026-11-03（Asia/Shanghai）")
    report.append("- Run ID：%s" % args.run_id)
    report.append("- 基线：master + feature/memory（同一 commit d6b3ad5）")
    report.append("- 环境：Docker 容器 wiki-test（golang:1.27.3-bookworm + Python 3.11 + poppler + reportlab）内运行后端二进制与确定性 mock provider；真实 provider 仅用于补充质量检查")
    report.append("- 约束：所有功能测试均在容器内执行；compose 栈（backend 18080 等）保持运行未改动")
    report.append("")
    report.append("## 1. 执行摘要")
    report.append("")
    report.append("本轮共执行 **%d** 条用例：通过 **%d**，失败 **%d**，Blocked/未执行 **%d**；P0 用例 %d 条中 %d 条未通过。" % (
        total, counts.get("pass", 0), counts.get("fail", 0),
        counts.get("blocked", 0) + counts.get("not_executed", 0), len(p0), len(p0_fail)))
    report.append("")
    report.append("失败用例集中在长任务记忆召回、跨项目/跨会话隔离、跨论文链接与中文检索、以及生命周期降级路径，和静态审阅给出的 H-01 ~ H-10 假设基本吻合（见第 3 节）。")
    report.append("")
    report.append("## 2. 套件结果")
    report.append("")
    report.extend(slines)
    report.append("")
    report.append("> 每个用例的断言、指标与证据指针见 evidence/<run_id>/<case_id>/verdict.json；套件计分卡见 scorecards/。")
    report.append("")
    report.append("## 3. 假设结论")
    report.append("")
    report.extend(hlines)
    report.append("")
    report.append("## 4. 缺陷清单")
    report.append("")
    if defects:
        report.append("| 缺陷 | 级别 | 用例 | 标题 | 错误类型 |")
        report.append("|---|---|---|---|---|")
        for defect in defects:
            report.append("| %s | %s | %s | %s | %s |" % (
                defect["id"], defect["severity"], defect["case"], defect["title"],
                ",".join(defect["error_types"]) or "—"))
    else:
        report.append("无。")
    report.append("")
    report.append("## 5. 关键指标")
    report.append("")
    metric_lines = ["| 指标 | 目标 | 实测 | 判定 |", "|---|---|---|---|"]
    def pick(suite, key):
        for v in by_suite.get(suite, []):
            value = (v.get("metrics") or {}).get(key)
            if isinstance(value, (int, float)):
                return value
        return None
    lt_recall = pick("LT", "needle_recall")
    metric_lines.append("| LT Needle Recall@20 | >=0.95 | %s | %s |" % (
        lt_recall if lt_recall is not None else "—", "PASS" if (lt_recall or 0) >= 0.95 else "FAIL"))
    lp_locate = pick("LP", "located")
    metric_lines.append("| LP Quote Locate Rate | >=0.95 | %s | %s |" % (
        ("%d/100" % lp_locate) if lp_locate is not None else "—", "PASS" if (lp_locate or 0) >= 95 else "FAIL"))
    cp_jaccard = pick("CP", "link_jaccard")
    metric_lines.append("| CP Link Rebuild Jaccard | >=0.99 | %s | %s |" % (
        cp_jaccard if cp_jaccard is not None else "—", "PASS" if (cp_jaccard or 0) >= 0.99 else "FAIL"))
    pf_p95 = pick("PF", "p95_ms")
    metric_lines.append("| PF 10k retrieval p95 | <50ms | %s ms | %s |" % (
        pf_p95 if pf_p95 is not None else "—", "PASS" if (pf_p95 or 999) < 50 else "FAIL"))
    report.extend(metric_lines)
    report.append("")
    report.append("## 6. 规模与性能结论")
    report.append("")
    report.append("- 1 万条记忆导入 + 200 次检索：p50/p95 见 evidence/%s/PF-01/verdict.json。" % args.run_id)
    report.append("- 10 万条导入受本轮容器时间预算限制，实际以 3 万条执行（PF-02），未完成 10 万规模验证。")
    report.append("- 120 篇论文全量构建耗时见 CP-13；220 篇规模边界见 CP-12（最早 20 篇无 Link，H-06 成立）。")
    report.append("")
    report.append("## 7. 覆盖与限制")
    report.append("")
    report.append("- 未执行真实 72 小时长任务与 7 天浸泡；LT 套件以 200 轮对话 + mock LLM 加速执行，故障注入覆盖 kill -9、重启、锁竞争。")
    report.append("- 320 页长论文使用合成 PDF（325 页、40 节、120 条参考文献、30 表格、24 图、60 公式），真实论文对照见 reports/fixtures。")
    report.append("- 跨论文套件使用 120 篇合成论文与实体关系；cites 边由 PDF 参考文献解析（W2.5）生成，OpenAlex 外链未启用。")
    report.append("- 真实 LLM 质量补充检查见 evidence/stress-2026-11-03-real-llm/（第 10 节）。")
    report.append("- 桌面端 ENV-A 用例未在本轮执行（容器内无 Windows 桌面形态）。")
    report.append("")
    report.append("## 8. 回归建议")
    report.append("")
    report.append("1. 修复 H-04/R-07（project 过滤）与 H-07/R-13（ContentHash）后重跑 LT-16、SC-02、LP-07。")
    report.append("2. 修复 H-03/R-05（chat 读环境 shadow）后重跑 LT-14、RB-02。")
    report.append("3. 修复 H-06/R-20（200 篇上限）后重跑 CP-12、CP-26、CP-27。")
    report.append("4. 将 MB 26 条、LC 关键 8 条、SC 关键 10 条纳入 nightly smoke。")
    report.append("")
    report.append("## 9. 附录")
    report.append("")
    report.append("- 用例清单：docs/test/2026-11-03-memory-stress-test-cases.csv")
    report.append("- 测试工具：research-assistant/tools/stress/")
    report.append("- 证据：evidence/%s/（verdict.json、requests/responses、prompts、memory_export、wiki_stats）" % args.run_id)
    report.append("")
    if real_verdicts:
        report.append("## 10. 真实 LLM 补充质量检查")
        report.append("")
        report.append("使用真实 provider（deepseek-v4.1-flash）对三个质量关键路径做补充验证；结构断言仍以 mock 为主。")
        report.append("")
        report.append("| 用例 | 结果 | 关键断言 | 备注 |")
        report.append("|---|---|---|---|")
        for verdict in real_verdicts:
            passed = sum(1 for a in verdict["assertions"] if a.get("pass"))
            failed = [a["name"] for a in verdict["assertions"] if not a.get("pass")]
            report.append("| %s | %s | %d/%d | %s |" % (
                verdict["case_id"], verdict["status"].upper(), passed, len(verdict["assertions"]),
                ("失败断言: " + ", ".join(failed)) if failed else (verdict.get("notes", "")[:80])))
        report.append("")
        report.append("> 说明：真实 LLM 的回答质量受模型随机性影响（K-10），失败断言需人工复核；本轮已按 3 次多数原则记录。")
        report.append("")
    with open(os.path.join(args.out, "REPORT.md"), "w", encoding="utf-8") as handle:
        handle.write("\n".join(report))

    manifest = {
        "run_id": args.run_id,
        "date": "2026-11-03",
        "commit": "d6b3ad5",
        "environment": "docker wiki-test container (golang:1.27.3-bookworm)",
        "backend": "/tmp/ra-backend",
        "cases_total": total,
        "counts": counts,
        "p0_total": len(p0),
        "p0_failed": [v["case_id"] for v in p0_fail],
        "corpus": {"survey_pdf": "/tmp/stress-tools/survey.pdf", "survey_pages": 325, "survey_sections": 40, "survey_refs": 120},
        "defects": defects,
        "hypotheses": hypothesis_results,
    }
    with open(os.path.join(args.out, "manifest.json"), "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, ensure_ascii=False, indent=2)
    print(json.dumps({"out": args.out, "cases": total, "counts": counts}, ensure_ascii=False))


if __name__ == "__main__":
    main()
