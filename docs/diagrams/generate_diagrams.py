#!/usr/bin/env python3
"""Generate the three project diagrams as editable draw.io files and SVG previews."""
from __future__ import annotations

import html
import re
from pathlib import Path

OUT = Path(__file__).resolve().parent

BOX = "rounded=1;whiteSpace=wrap;html=1;fillColor=#dae8fc;strokeColor=#6c8ebf;fontSize=12;"
GROUP = "rounded=0;whiteSpace=wrap;html=1;fillColor=#f8f9fa;strokeColor=#adb5bd;verticalAlign=top;fontStyle=1;fontSize=14;"
AGENT = "rounded=1;whiteSpace=wrap;html=1;fillColor=#d5e8d4;strokeColor=#82b366;fontSize=12;"
EXTERNAL = "rounded=1;whiteSpace=wrap;html=1;fillColor=#e1d5e7;strokeColor=#9673a6;fontSize=12;"
STORE = "shape=cylinder3;whiteSpace=wrap;html=1;boundedLbl=1;backgroundOutline=1;size=15;fillColor=#ffe6cc;strokeColor=#d79b00;fontSize=12;"
DECISION = "rhombus;whiteSpace=wrap;html=1;fillColor=#fff2cc;strokeColor=#d6b656;fontSize=12;"
NOTE = "shape=note;whiteSpace=wrap;html=1;backgroundOutline=1;darkOpacity=0.05;fillColor=#fff2cc;strokeColor=#d6b656;fontSize=11;"
TITLE = "text;html=1;fontSize=20;fontStyle=1;align=center;verticalAlign=middle;"
EDGE = "edgeStyle=orthogonalEdgeStyle;rounded=0;html=1;endArrow=block;strokeWidth=1.4;fontSize=11;"
EDGE_DASH = "edgeStyle=orthogonalEdgeStyle;rounded=0;html=1;endArrow=block;strokeWidth=1.4;dashed=1;fontSize=11;"


def n(i, label, x, y, w, h, style=BOX):
    return {"id": i, "label": label, "x": x, "y": y, "w": w, "h": h, "style": style}


def e(i, frm, to, label="", dashed=False):
    return {"id": i, "from": frm, "to": to, "label": label, "style": EDGE_DASH if dashed else EDGE}


def architecture():
    nodes = [
        n("T", "Research Assistant Agent — System Architecture", 40, 20, 1520, 42, TITLE),
        n("G-USERS", "Users / Clients", 40, 90, 280, 180, GROUP),
        n("U1", "Researcher", 60, 130, 240, 38),
        n("U2", "Web Browser", 60, 180, 240, 38),
        n("U3", "API / Script Client", 60, 230, 240, 38),
        n("G-FE", "Frontend / React Workbench", 40, 310, 280, 540, GROUP),
        n("FE1", "Research Assistant App", 60, 350, 240, 46),
        n("FE2", "Chat + PDF Reader", 60, 412, 240, 46),
        n("FE3", "DAG / Plan Graph", 60, 474, 240, 46),
        n("FE4", "Execution Sidebar", 60, 536, 240, 46),
        n("FE5", "Benchmark / AutoResearch Views", 60, 598, 240, 46),
        n("FE6", "SSE Event Client", 60, 660, 240, 46),
        n("FE7", "Claim Evidence Graph", 60, 722, 240, 46),
        n("G-BE", "Go Backend / API + Orchestration", 380, 90, 760, 760, GROUP),
        n("G-API", "HTTP API Layer", 400, 140, 720, 140, GROUP),
        n("A1", "Auth / CORS", 420, 180, 160, 48),
        n("A2", "Plan API", 600, 180, 160, 48),
        n("A3", "Upload / Document API", 780, 180, 160, 48),
        n("A4", "SSE / Tickets", 960, 180, 140, 48),
        n("G-CORE", "Planning & Scheduling", 400, 310, 720, 230, GROUP),
        n("C1", "Intent Router", 420, 350, 200, 46),
        n("C2", "Planner + DAG Validator", 640, 350, 210, 46),
        n("C3", "Plan Store (JSONL)", 870, 350, 230, 46),
        n("C4", "Scheduler", 420, 430, 200, 46),
        n("C5", "Routed Task Executor", 640, 430, 210, 46),
        n("C6", "Event Bus / SSE", 870, 430, 230, 46),
        n("C7", "Lease / Retry / Cancel / Budget / Approval", 420, 500, 680, 28, NOTE),
        n("G-AGENTS", "Specialized Agents", 400, 580, 720, 250, GROUP),
        n("AG1", "Chat", 420, 620, 160, 46, AGENT),
        n("AG2", "Librarian", 600, 620, 160, 46, AGENT),
        n("AG3", "Coder", 780, 620, 160, 46, AGENT),
        n("AG4", "Research Coding", 960, 620, 140, 46, AGENT),
        n("AG5", "Benchmark", 420, 690, 160, 46, AGENT),
        n("AG6", "Data", 600, 690, 160, 46, AGENT),
        n("AG7", "AutoResearch", 780, 690, 160, 46, AGENT),
        n("AG8", "Ablation ToT", 960, 690, 140, 46, AGENT),
        n("E1", "LLM Provider\nOpenAI-compatible\n(DeepSeek / Qwen)", 1220, 110, 320, 120, EXTERNAL),
        n("E2", "Research Optimizer\nPython + SQLite\ncandidate ranking", 1220, 280, 320, 120, EXTERNAL),
        n("G-STORE", "Persistent State", 1220, 450, 320, 300, GROUP),
        n("S1", "Plan store volume", 1240, 490, 280, 44, STORE),
        n("S2", "Upload volume", 1240, 548, 280, 44, STORE),
        n("S3", "Benchmark private volume", 1240, 606, 280, 44, STORE),
        n("S4", "Optimizer SQLite", 1240, 664, 280, 44, STORE),
        n("G-SB", "Docker Sandbox (isolated execution plane)", 380, 900, 760, 190, GROUP),
        n("SB1", "Sandbox Gateway\nauth + reverse proxy", 400, 940, 220, 58),
        n("SB2", "Sandbox Executor\nDocker socket + /tmp", 660, 940, 220, 58),
        n("SB3", "Docker Containers\npython:3.11", 920, 940, 200, 58),
        n("SB4", "gateway has no Docker socket; executor is not published", 400, 1020, 720, 34, NOTE),
    ]
    edges = [
        e("e1", "U1", "FE1", "goal / files"), e("e2", "U2", "FE1"), e("e3", "U3", "A2", "REST"),
        e("e4", "FE1", "A2", "POST /api/plan"), e("e5", "FE6", "A4", "SSE"),
        e("e6", "A2", "C1", "intent"), e("e7", "C1", "C2", "IntentContext"),
        e("e8", "C2", "C3", "PlanGraph"), e("e9", "C3", "C4", "load"),
        e("e10", "C4", "C5", "dispatch"), e("e11", "C5", "G-AGENTS", "route by assigned_to"),
        e("e12", "AG2", "SB1", "sandbox client"), e("e13", "AG4", "SB1", "sandbox client"),
        e("e14", "SB1", "SB2", "forward"), e("e15", "SB2", "SB3", "docker run / exec"),
        e("e16", "SB3", "C5", "stdout / metrics / files", True),
        e("e17", "C2", "E1", "LLM planning"), e("e18", "AG2", "E1", "LLM calls"),
        e("e19", "C5", "E2", "candidate ranking"), e("e20", "E2", "S4"),
        e("e21", "C3", "S1"), e("e22", "A3", "S2", "uploads"), e("e23", "C5", "S3", "hidden labels"),
        e("e24", "C4", "C6", "plan/task events"), e("e25", "C6", "FE6", "events"),
        e("e26", "FE2", "FE7", "render"),
    ]
    return {"id": "architecture", "name": "System Architecture", "width": 1600, "height": 1120, "nodes": nodes, "edges": edges}


def runtime_flow():
    nodes = [
        n("T", "Runtime Flow — from a research goal to auditable artifacts", 40, 20, 1520, 42, TITLE),
        n("L1", "User", 30, 90, 210, 980, GROUP), n("L2", "Frontend", 250, 90, 210, 980, GROUP),
        n("L3", "Backend API", 470, 90, 210, 980, GROUP), n("L4", "Scheduler", 690, 90, 210, 980, GROUP),
        n("L5", "Agent", 910, 90, 210, 980, GROUP), n("L6", "Sandbox", 1130, 90, 210, 980, GROUP),
        n("L7", "Store / SSE", 1350, 90, 210, 980, GROUP),
        n("U1", "提交研究目标", 50, 140, 170, 44), n("U2", "查看 DAG / 审批", 50, 330, 170, 44), n("U3", "查看日志与产物", 50, 820, 170, 44),
        n("F1", "POST /api/plan", 270, 140, 170, 44), n("F2", "渲染 PlanGraph", 270, 240, 170, 44),
        n("F3", "POST /execute", 270, 370, 170, 44), n("F4", "SSE 订阅 / 轮询", 270, 720, 170, 44),
        n("F5", "渲染节点状态", 270, 820, 170, 44),
        n("B1", "Auth / CORS", 490, 140, 170, 44), n("B2", "Intent Router", 490, 200, 170, 44),
        n("B3", "Planner + DAG Validator", 490, 260, 170, 44), n("B4", "Plan Store 保存", 490, 320, 170, 44),
        n("B5", "启动 Scheduler", 490, 400, 170, 44), n("B6", "终态判定", 490, 900, 170, 44),
        n("S1", "加载 PlanGraph", 710, 400, 170, 44), n("S2", "就绪节点入队", 710, 460, 170, 44),
        n("S3", "Dispatch Task", 710, 520, 170, 44), n("S4", "Lease / Timeout / Retry", 710, 580, 170, 44),
        n("S5", "更新节点与 Artifact", 710, 720, 170, 44), n("S6", "完成 / 失败 / 取消", 710, 840, 170, 44),
        n("A1", "RoutedTaskExecutor", 930, 520, 170, 44), n("A2", "Agent 执行", 930, 580, 170, 44),
        n("A3", "结果校验", 930, 660, 170, 44), n("A4", "Artifact 构建", 930, 720, 170, 44),
        n("X1", "Gateway 鉴权", 1150, 580, 170, 44), n("X2", "Executor 执行", 1150, 640, 170, 44),
        n("X3", "Docker 容器", 1150, 700, 170, 44), n("X4", "stdout / metrics / files", 1150, 760, 170, 44),
        n("T1", "Plan JSONL", 1370, 320, 170, 44, STORE), n("T2", "Event Bus", 1370, 720, 170, 44),
        n("T3", "SSE 推送", 1370, 780, 170, 44), n("T4", "事件历史回放", 1370, 840, 170, 44),
        n("NOTE", "cancel / retry / timeout 在任何阶段都写入终态事件；终态不允许被迟到结果复活。", 40, 1000, 1520, 50, NOTE),
    ]
    edges = [
        e("r1", "U1", "F1", "输入"), e("r2", "F1", "B1", "HTTPS"), e("r3", "B1", "B2"),
        e("r4", "B2", "B3", "意图"), e("r5", "B3", "B4", "PlanGraph"), e("r6", "B4", "T1"),
        e("r7", "B3", "F2", "plan_graph"), e("r8", "F2", "U2", "展示"), e("r9", "U2", "F3", "批准/执行"),
        e("r10", "F3", "B5"), e("r11", "B5", "S1"), e("r12", "T1", "S1", "load"),
        e("r13", "S1", "S2"), e("r14", "S2", "S3", "ready"), e("r15", "S3", "A1", "dispatch"),
        e("r16", "A1", "A2", "resolve"), e("r17", "A2", "X1", "sandbox API"), e("r18", "X1", "X2"),
        e("r19", "X2", "X3", "docker exec"), e("r20", "X3", "X4", "stream"), e("r21", "X4", "A3", "结果", True),
        e("r22", "A3", "A4", "valid"), e("r23", "A4", "S5", "artifacts"), e("r24", "S3", "S4", "lease"),
        e("r25", "S4", "S6", "超时/取消"), e("r26", "S5", "S6", "terminal"), e("r27", "S6", "B6"),
        e("r28", "S5", "T2", "events"), e("r29", "T2", "T3"), e("r30", "T3", "F4", "SSE"),
        e("r31", "F4", "F5"), e("r32", "F5", "U3", "render"), e("r33", "T2", "T4", "replay"),
        e("r34", "B6", "T2", "final event"),
    ]
    return {"id": "runtime-flow", "name": "Runtime Flow", "width": 1600, "height": 1080, "nodes": nodes, "edges": edges}


def agent_flow():
    nodes = [
        n("T", "Agent Execution Flow — routing, sandboxing, artifacts and research loops", 40, 20, 1520, 42, TITLE),
        n("START", "Scheduler TaskNode\n(type / assigned_to / inputs)", 650, 70, 300, 58),
        n("ROUTER", "RoutedTaskExecutor\nresolveRunner()", 650, 160, 300, 52),
        n("DEC", "Agent type?", 680, 245, 240, 90, DECISION),
        n("AG1", "Chat\nconversation.respond", 40, 380, 220, 62, AGENT),
        n("AG2", "Librarian\npaper parse / claim rubric", 290, 380, 220, 62, AGENT),
        n("AG3", "Coder\nrepo / dependency / code", 540, 380, 220, 62, AGENT),
        n("AG4", "Research Coding\npaper debug / benchmark / AutoResearch", 790, 380, 220, 62, AGENT),
        n("AG5", "Benchmark\naudit / split / contract", 1040, 380, 220, 62, AGENT),
        n("AG6", "Data\nmetrics / comparison / evidence", 1290, 380, 220, 62, AGENT),
        n("SB", "Sandbox Client → Gateway → Executor → Docker Container", 300, 510, 1000, 62),
        n("ART", "Artifact Builder\n(non-empty, typed, producer-bound)", 650, 630, 300, 56),
        n("VALID", "Validate Artifact\nunique producer / contract", 650, 720, 300, 56),
        n("STORE", "PlanStore + Event Bus + SSE", 650, 810, 300, 52, STORE),
        n("TERM", "Task terminal status\ncompleted / failed / canceled", 650, 900, 300, 52),
        n("CLM", "Claim Evidence loop", 40, 620, 240, 330, GROUP),
        n("C1", "Freeze claim rubric", 60, 660, 200, 40), n("C2", "Collect evidence inventory", 60, 712, 200, 40),
        n("C3", "LLM adjudication per criterion", 60, 764, 200, 40), n("C4", "Verdict graph + coverage", 60, 816, 200, 40),
        n("AUTO", "AutoResearch loop", 1320, 620, 240, 430, GROUP),
        n("R1", "Freeze ResearchSpec", 1340, 660, 200, 38), n("R2", "Run baseline", 1340, 708, 200, 38),
        n("R3", "Propose candidate patch", 1340, 756, 200, 38), n("R4", "Evaluate candidate", 1340, 804, 200, 38),
        n("R5", "Keep / Reject + rollback", 1340, 852, 200, 38), n("R6", "Hidden holdout validation", 1340, 900, 200, 38),
        n("NOTE", "所有 Agent 的代码/命令都只通过沙箱执行；Artifact 为空、重复生产者或契约不匹配时任务失败，而不是伪造成功。", 300, 1000, 1000, 52, NOTE),
    ]
    edges = [
        e("a1", "START", "ROUTER"), e("a2", "ROUTER", "DEC"),
        e("a3", "DEC", "AG1"), e("a4", "DEC", "AG2"), e("a5", "DEC", "AG3"),
        e("a6", "DEC", "AG4"), e("a7", "DEC", "AG5"), e("a8", "DEC", "AG6"),
        e("a9", "AG1", "SB"), e("a10", "AG2", "SB"), e("a11", "AG3", "SB"),
        e("a12", "AG4", "SB"), e("a13", "AG5", "SB"), e("a14", "AG6", "SB"),
        e("a15", "SB", "ART"), e("a16", "ART", "VALID"), e("a17", "VALID", "STORE"), e("a18", "STORE", "TERM"),
        e("a19", "AG2", "C1", "claim"), e("a20", "C1", "C2"), e("a21", "C2", "C3"), e("a22", "C3", "C4"),
        e("a23", "C4", "ART", "graph"),
        e("a24", "AG4", "R1", "AutoResearch"), e("a25", "R1", "R2"), e("a26", "R2", "R3"), e("a27", "R3", "R4"),
        e("a28", "R4", "R5"), e("a29", "R5", "R3", "reject"), e("a30", "R5", "R6", "keep"),
        e("a31", "R6", "ART", "validation"),
    ]
    return {"id": "agent-execution-flow", "name": "Agent Execution Flow", "width": 1600, "height": 1080, "nodes": nodes, "edges": edges}


def drawio_xml(diagram):
    cells = ['<mxCell id="0" />', '<mxCell id="1" parent="0" />']
    for node in diagram["nodes"]:
        cells.append(
            '<mxCell id="%s" value="%s" style="%s" vertex="1" parent="1">'
            '<mxGeometry x="%s" y="%s" width="%s" height="%s" as="geometry" /></mxCell>'
            % (html.escape(node["id"], quote=True), html.escape(node["label"], quote=True).replace("\n", "&#10;"),
               html.escape(node["style"], quote=True), node["x"], node["y"], node["w"], node["h"])
        )
    for edge in diagram["edges"]:
        cells.append(
            '<mxCell id="%s" value="%s" style="%s" edge="1" parent="1" source="%s" target="%s">'
            '<mxGeometry relative="1" as="geometry" /></mxCell>'
            % (html.escape(edge["id"], quote=True), html.escape(edge["label"], quote=True),
               html.escape(edge["style"], quote=True), html.escape(edge["from"], quote=True), html.escape(edge["to"], quote=True))
        )
    return (
        '<mxfile host="app.diagrams.net" modified="2026-10-23T00:00:00.000Z" agent="research-assistant" version="24.7.17" type="device">\n'
        '  <diagram id="%s" name="%s">\n'
        '    <mxGraphModel dx="1600" dy="1080" grid="1" gridSize="10" guides="1" tooltips="1" connect="1" arrows="1" fold="1" page="1" pageScale="1" pageWidth="%s" pageHeight="%s" math="0" shadow="0">\n'
        '      <root>\n        %s\n      </root>\n    </mxGraphModel>\n  </diagram>\n</mxfile>\n'
        % (diagram["id"], html.escape(diagram["name"], quote=True), diagram["width"], diagram["height"], "\n        ".join(cells))
    )


def color(style, key, fallback):
    match = re.search(key + r"=(#[0-9a-fA-F]{6})", style)
    return match.group(1) if match else fallback


def svg(diagram):
    width, height = diagram["width"], diagram["height"]
    parts = [
        '<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">' % (width, height, width, height),
        '<defs><marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L0,6 L9,3 z" fill="#495057"/></marker></defs>',
        '<rect width="100%" height="100%" fill="#ffffff"/>',
    ]
    nodes = {node["id"]: node for node in diagram["nodes"]}
    groups = [node for node in diagram["nodes"] if node["style"] == GROUP]
    others = [node for node in diagram["nodes"] if node["style"] != GROUP]
    for node in groups:
        fill = color(node["style"], "fillColor", "#f8f9fa")
        stroke = color(node["style"], "strokeColor", "#adb5bd")
        parts.append('<rect x="%d" y="%d" width="%d" height="%d" fill="%s" stroke="%s" stroke-width="1.2"/>' % (node["x"], node["y"], node["w"], node["h"], fill, stroke))
        parts.append('<text x="%d" y="%d" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="14" font-weight="700" fill="#343a40">%s</text>' % (node["x"] + 12, node["y"] + 22, html.escape(node["label"])))
    for edge in diagram["edges"]:
        a, b = nodes.get(edge["from"]), nodes.get(edge["to"])
        if not a or not b:
            continue
        x1, y1 = a["x"] + a["w"] / 2, a["y"] + a["h"] / 2
        x2, y2 = b["x"] + b["w"] / 2, b["y"] + b["h"] / 2
        dash = ' stroke-dasharray="6 4"' if "dashed=1" in edge["style"] else ""
        parts.append('<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#495057" stroke-width="1.3"%s marker-end="url(#arrow)"/>' % (x1, y1, x2, y2, dash))
        if edge["label"]:
            mx, my = (x1 + x2) / 2, (y1 + y2) / 2
            parts.append('<text x="%.1f" y="%.1f" text-anchor="middle" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="11" fill="#495057" paint-order="stroke" stroke="#ffffff" stroke-width="4">%s</text>' % (mx, my - 4, html.escape(edge["label"])))
    for node in others:
        fill = color(node["style"], "fillColor", "#dae8fc")
        stroke = color(node["style"], "strokeColor", "#6c8ebf")
        rx = 8 if "rhombus" not in node["style"] else 0
        if "rhombus" in node["style"]:
            cx, cy = node["x"] + node["w"] / 2, node["y"] + node["h"] / 2
            points = "%d,%d %d,%d %d,%d %d,%d" % (cx, node["y"], node["x"] + node["w"], cy, cx, node["y"] + node["h"], node["x"], cy)
            parts.append('<polygon points="%s" fill="%s" stroke="%s" stroke-width="1.2"/>' % (points, fill, stroke))
        else:
            parts.append('<rect x="%d" y="%d" width="%d" height="%d" rx="%d" fill="%s" stroke="%s" stroke-width="1.2"/>' % (node["x"], node["y"], node["w"], node["h"], rx, fill, stroke))
        lines = node["label"].split("\n")
        font = 20 if node["style"] == TITLE else (14 if node["style"] == GROUP else 12)
        if node["style"] == TITLE:
            parts.append('<text x="%d" y="%d" text-anchor="middle" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="%d" font-weight="700" fill="#212529">%s</text>' % (node["x"] + node["w"] / 2, node["y"] + node["h"] / 2 + 7, font, html.escape(node["label"])))
            continue
        start_y = node["y"] + node["h"] / 2 - (len(lines) - 1) * 7
        for idx, line in enumerate(lines):
            parts.append('<text x="%d" y="%.1f" text-anchor="middle" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="%d" fill="#212529">%s</text>' % (node["x"] + node["w"] / 2, start_y + idx * 15, font, html.escape(line)))
    parts.append('</svg>\n')
    return "\n".join(parts)


def main():
    for factory in (architecture, runtime_flow, agent_flow):
        diagram = factory()
        (OUT / (diagram["id"] + ".drawio")).write_text(drawio_xml(diagram), encoding="utf-8")
        (OUT / (diagram["id"] + ".svg")).write_text(svg(diagram), encoding="utf-8")
        print("wrote", diagram["id"] + ".drawio", diagram["id"] + ".svg")


if __name__ == "__main__":
    main()
