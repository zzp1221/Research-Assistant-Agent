#!/usr/bin/env python3
"""Generate the three project diagrams as editable draw.io files and SVG previews.

The layout data is intentionally declarative: edit the architecture(),
runtime_flow() and agent_flow() functions, then run this script. The SVG
renderer uses orthogonal routing so the preview stays readable without a
draw.io installation.
"""
from __future__ import annotations

import html
from pathlib import Path

OUT = Path(__file__).resolve().parent

BOX = "rounded=1;whiteSpace=wrap;html=1;fillColor=#dae8fc;strokeColor=#6c8ebf;fontSize=12;"
GROUP = "rounded=0;whiteSpace=wrap;html=1;fillColor=#f8f9fa;strokeColor=#adb5bd;verticalAlign=top;fontStyle=1;fontSize=14;"
AGENT = "rounded=1;whiteSpace=wrap;html=1;fillColor=#d5e8d4;strokeColor=#82b366;fontSize=12;"
EXTERNAL = "rounded=1;whiteSpace=wrap;html=1;fillColor=#e1d5e7;strokeColor=#9673a6;fontSize=12;"
STORE = "shape=cylinder3;whiteSpace=wrap;html=1;boundedLbl=1;backgroundOutline=1;size=15;fillColor=#ffe6cc;strokeColor=#d79b00;fontSize=12;"
SECURITY = "rounded=1;whiteSpace=wrap;html=1;fillColor=#f8cecc;strokeColor=#b85450;fontSize=12;"
DECISION = "rhombus;whiteSpace=wrap;html=1;fillColor=#fff2cc;strokeColor=#d6b656;fontSize=12;"
NOTE = "shape=note;whiteSpace=wrap;html=1;backgroundOutline=1;darkOpacity=0.05;fillColor=#fff2cc;strokeColor=#d6b656;fontSize=11;"
TITLE = "text;html=1;fontSize=20;fontStyle=1;align=center;verticalAlign=middle;"
EDGE = "edgeStyle=orthogonalEdgeStyle;rounded=0;html=1;endArrow=block;strokeWidth=1.4;fontSize=11;"
EDGE_DASH = "edgeStyle=orthogonalEdgeStyle;rounded=0;html=1;endArrow=block;strokeWidth=1.4;dashed=1;fontSize=11;"


def n(i, label, x, y, w, h, style=BOX):
    return {"id": i, "label": label, "x": x, "y": y, "w": w, "h": h, "style": style}


def e(i, frm, to, label="", dashed=False, from_side="", to_side=""):
    return {
        "id": i,
        "from": frm,
        "to": to,
        "label": label,
        "style": EDGE_DASH if dashed else EDGE,
        "fromSide": from_side,
        "toSide": to_side,
    }


def architecture():
    nodes = [
        n("T", "Research Assistant Agent — System Architecture", 40, 16, 1680, 40, TITLE),

        # Clients
        n("G-CLIENT", "Clients", 40, 78, 360, 170, GROUP),
        n("U1", "Researcher / Desktop user", 60, 116, 320, 38),
        n("U2", "Web Browser", 60, 164, 150, 38),
        n("U3", "API / CLI client", 230, 164, 150, 38),

        # Desktop shell
        n("G-DESK", "Windows Desktop (MyGo 0.7.4)", 40, 268, 360, 262, GROUP),
        n("D1", "MyGo window (WebView2)", 60, 306, 320, 38),
        n("D2", "bridge (mygo.Bind)", 60, 354, 150, 36),
        n("D3", "supervisor\n(Job Object)", 230, 354, 150, 36),
        n("D4", "loopback proxy (SSE)", 60, 400, 150, 36),
        n("D5", "settings + DPAPI", 230, 400, 150, 36),
        n("D6", "updater · diagnostics · tray", 60, 446, 320, 36),

        # Frontend
        n("G-FE", "React Workbench", 40, 550, 360, 190, GROUP),
        n("F1", "Chat · DAG · PDF · Literature\nPlans · Review · Cost · Memory · Settings", 60, 588, 320, 52),
        n("F2", "SSE event client", 60, 652, 150, 36),
        n("F3", "desktop bridge", 230, 652, 150, 36),

        # Backend
        n("G-BE", "Go Backend — API, cognition, governance, scheduling, execution", 440, 78, 760, 780, GROUP),
        n("G-API", "API layer", 460, 116, 720, 96, GROUP),
        n("A1", "Gin API · auth · CORS", 480, 150, 220, 40),
        n("A2", "Uploads · documents", 720, 150, 220, 40),
        n("A3", "SSE tickets · events", 960, 150, 200, 40),

        n("G-COG", "Cognition & governance", 460, 230, 720, 230, GROUP),
        n("C1", "Intent Router", 480, 266, 160, 40),
        n("C2", "Complexity Router L0–L4", 660, 266, 180, 40),
        n("C3", "Planner + DAG Validator", 860, 266, 300, 40),
        n("C4", "Revision Store + Review Gate", 480, 326, 300, 40),
        n("C5", "Plan Store (snapshot + JSONL)", 800, 326, 360, 40),
        n("C6", "Budget · Usage · Audit", 480, 386, 680, 40),

        n("G-SCH", "Scheduling & execution", 460, 478, 720, 230, GROUP),
        n("S1", "Scheduler (lease · retry · cancel · budget)", 480, 514, 340, 40),
        n("S2", "Runtime Gate (approval)", 840, 514, 320, 40),
        n("S3", "RoutedTaskExecutor", 480, 574, 220, 40),
        n("S4", "AgentProfile + PromptVersion", 720, 574, 220, 40),
        n("S5", "Memory injection\n(core / retrieved)", 960, 574, 200, 40),
        n("S6", "ToolInvoker (native / HTTP / MCP)", 480, 634, 340, 40),
        n("S7", "MemorySink + PlanFinalizer", 840, 634, 320, 40),

        # Agents
        n("G-AG", "Specialized agents (12 roles, routed by assigned_to)", 440, 886, 760, 196, GROUP),
        n("AG1", "Chat / General", 460, 922, 170, 38, AGENT),
        n("AG2", "Librarian", 650, 922, 170, 38, AGENT),
        n("AG3", "Benchmark", 840, 922, 170, 38, AGENT),
        n("AG4", "Coder", 1030, 922, 150, 38, AGENT),
        n("AG5", "Research Coding", 460, 970, 170, 38, AGENT),
        n("AG6", "Data", 650, 970, 170, 38, AGENT),
        n("AG7", "AutoResearch", 840, 970, 170, 38, AGENT),
        n("AG8", "Experiment Research", 1030, 970, 150, 38, AGENT),
        n("AG9", "Ablation ToT", 460, 1018, 170, 38, AGENT),
        n("AG10", "Paper Debug", 650, 1018, 170, 38, AGENT),
        n("AG11", "Visual Verifier", 840, 1018, 170, 38, AGENT),
        n("AG12", "Answer", 1030, 1018, 150, 38, AGENT),

        # Sandbox
        n("G-SB", "Sandbox service — control plane + execution plane", 440, 1110, 760, 180, GROUP),
        n("SB1", "gateway (auth + proxy)", 460, 1148, 220, 46, SECURITY),
        n("SB2", "executor (privileged)", 700, 1148, 220, 46, SECURITY),
        n("SB3", "Provider: docker · native\nvm · remote", 940, 1148, 240, 46),
        n("SB4", "gateway has no Docker socket; executor is unpublished", 460, 1214, 720, 30, NOTE),

        # External
        n("G-EXT", "External dependencies", 1240, 78, 480, 280, GROUP),
        n("E1", "LLM provider — OpenAI-compatible (DeepSeek default)", 1260, 116, 440, 44, EXTERNAL),
        n("E2", "Research Optimizer — Python + SQLite", 1260, 172, 440, 44, EXTERNAL),
        n("E3", "Literature sources\narXiv · OpenAlex · Crossref · PubMed · S2 · DBLP", 1260, 228, 440, 44, EXTERNAL),
        n("E4", "Docker daemon · WSL2 · OpenSandbox", 1260, 284, 440, 44, EXTERNAL),

        # Persistent state
        n("G-STORE", "Persistent state", 1240, 390, 480, 380, GROUP),
        n("P1", "Plan + revision store", 1260, 426, 440, 40, STORE),
        n("P2", "Memory store (JSONL + BM25 + snapshots)", 1260, 478, 440, 40, STORE),
        n("P3", "Upload / document store", 1260, 530, 440, 40, STORE),
        n("P4", "Literature library", 1260, 582, 440, 40, STORE),
        n("P5", "Budget / usage / audit", 1260, 634, 440, 40, STORE),
        n("P6", "Artifact / blob store", 1260, 686, 440, 40, STORE),

        n("G-DSTATE", "Desktop local state", 1240, 800, 480, 160, GROUP),
        n("P7", "settings.json · secrets.dat (DPAPI)", 1260, 836, 440, 36, STORE),
        n("P8", "runtimes · workspaces · logs · crash dumps", 1260, 886, 440, 36, STORE),

        n("NOTE", "Runtime permission (confirm_important / full_access) gates image pulls, dependency installs, downloads, GPU and network.\nSandbox capabilities are negotiated per provider; missing capabilities return blocked + missing_requirements, never silent mock success.", 440, 1320, 1280, 70, NOTE),
    ]
    edges = [
        e("e1", "U1", "D1", "launch", from_side="right", to_side="left"),
        e("e2", "U2", "F1", "HTTPS", from_side="right", to_side="left"),
        e("e3", "U3", "A1", "REST / CLI", from_side="right", to_side="left"),
        e("e4", "D1", "D4", "loads", from_side="bottom", to_side="top"),
        e("e5", "D4", "F1", "same-origin", from_side="right", to_side="left"),
        e("e6", "F1", "A1", "REST", from_side="right", to_side="left"),
        e("e7", "F2", "A3", "SSE", from_side="right", to_side="left", dashed=True),
        e("e9", "A1", "C1", "intent", from_side="bottom", to_side="top"),
        e("e10", "C1", "C2", "classify", from_side="right", to_side="left"),
        e("e11", "C2", "C3", "route", from_side="right", to_side="left"),
        e("e12", "C3", "C4", "PlanGraph", from_side="bottom", to_side="top"),
        e("e13", "C4", "C5", "save / revision", from_side="right", to_side="left"),
        e("e14", "C5", "S1", "execute", from_side="bottom", to_side="top"),
        e("e15", "S1", "S3", "dispatch", from_side="bottom", to_side="top"),
        e("e16", "S3", "S4", "resolve profile", from_side="right", to_side="left"),
        e("e17", "S4", "S5", "inject", from_side="right", to_side="left"),
        e("e18", "S5", "G-AG", "invoke", from_side="bottom", to_side="top"),
        e("e19", "G-AG", "S6", "tools", from_side="left", to_side="bottom"),
        e("e20", "G-AG", "SB1", "sandbox client", from_side="bottom", to_side="top"),
        e("e21", "SB1", "SB2", "proxy /api/v1", from_side="right", to_side="left"),
        e("e22", "SB2", "SB3", "provider", from_side="right", to_side="left"),
        e("e23", "SB3", "S7", "stdout / metrics / artifacts", from_side="right", to_side="bottom", dashed=True),
        e("e24", "S7", "F2", "events", from_side="right", to_side="right", dashed=True),
        e("e29", "G-AG", "E1", "LLM calls", from_side="right", to_side="left"),
        e("e35", "S7", "P1", "persist outcome", from_side="right", to_side="left", dashed=True),
    ]
    return {"id": "architecture", "name": "System Architecture", "width": 1760, "height": 1420, "nodes": nodes, "edges": edges}


def runtime_flow():
    nodes = [
        n("T", "Runtime Flow — from research goal to auditable artifacts", 40, 16, 1680, 40, TITLE),
        n("S1", "1 · Input", 40, 90, 200, 760, GROUP),
        n("I1", "Research goal", 60, 130, 160, 42),
        n("I2", "Upload PDF / CSV\n/ dataset", 60, 188, 160, 42),
        n("I3", "Repository / DOI\n/ arXiv", 60, 246, 160, 42),
        n("I4", "Runtime permission", 60, 304, 160, 42),
        n("I5", "Session / project\ncontext", 60, 362, 160, 42),

        n("S2", "2 · Context", 250, 90, 200, 760, GROUP),
        n("C1", "AttachmentRef /\nDocumentRef", 270, 130, 160, 42),
        n("C2", "PaperMetadata +\nDocumentStore", 270, 188, 160, 42),
        n("C3", "Literature library", 270, 246, 160, 42),
        n("C4", "Memory retrieval\n(BM25 / RRF)", 270, 304, 160, 42),
        n("C5", "ResearchContext +\nInputBindings", 270, 362, 160, 42),

        n("S3", "3 · Intent & plan", 460, 90, 200, 760, GROUP),
        n("P1", "Intent Router", 480, 130, 160, 42),
        n("P2", "Complexity Router\nL0–L4", 480, 188, 160, 42),
        n("P3", "Planner +\ntemplate variant", 480, 246, 160, 42),
        n("P4", "DAG Validator", 480, 304, 160, 42),
        n("P5", "PlanGraph", 480, 362, 160, 42),

        n("S4", "4 · Revision & review", 670, 90, 200, 760, GROUP),
        n("R1", "Immutable revision\nr1 / r2…", 690, 130, 160, 42),
        n("R2", "Review gate", 690, 188, 160, 42),
        n("R3", "Plan review /\ndiff / edit", 690, 246, 160, 42),
        n("R4", "Structured questions", 690, 304, 160, 42),
        n("R5", "Approve / reject", 690, 362, 160, 42),

        n("S5", "5 · Schedule", 880, 90, 200, 760, GROUP),
        n("Q1", "Load PlanGraph\n+ revision", 900, 130, 160, 42),
        n("Q2", "Scheduler leases", 900, 188, 160, 42),
        n("Q3", "Ready queue /\nconcurrency", 900, 246, 160, 42),
        n("Q4", "Runtime Gate", 900, 304, 160, 42),
        n("Q5", "RuntimeAction\napprove / reject", 900, 362, 160, 42),

        n("S6", "6 · Agent & memory", 1090, 90, 200, 760, GROUP),
        n("A1", "RoutedTaskExecutor", 1110, 130, 160, 42),
        n("A2", "AgentProfile +\nPromptVersion", 1110, 188, 160, 42),
        n("A3", "Memory injection\ncore / retrieved", 1110, 246, 160, 42),
        n("A4", "Specialized agent", 1110, 304, 160, 42),
        n("A5", "ToolInvoker\nnative / HTTP / MCP", 1110, 362, 160, 42),

        n("S7", "7 · Sandbox", 1300, 90, 200, 760, GROUP),
        n("X1", "Sandbox gateway", 1320, 130, 160, 42),
        n("X2", "Sandbox executor", 1320, 188, 160, 42),
        n("X3", "Provider capability\ncheck", 1320, 246, 160, 42),
        n("X4", "docker / native\n/ vm / remote", 1320, 304, 160, 42),
        n("X5", "stdout / metrics\n/ files", 1320, 362, 160, 42),

        n("S8", "8 · Accept & observe", 1510, 90, 210, 760, GROUP),
        n("Z1", "Artifact validation", 1530, 130, 170, 42),
        n("Z2", "PlanFinalizer /\nPlanOutcome", 1530, 188, 170, 42),
        n("Z3", "MemorySink\nRecordTask / Plan", 1530, 246, 170, 42),
        n("Z4", "Event Bus / SSE", 1530, 304, 170, 42),
        n("Z5", "Result panel / export", 1530, 362, 170, 42),

        n("T1", "Plan / revision store", 480, 500, 160, 44, STORE),
        n("T2", "Memory store", 1110, 500, 160, 44, STORE),
        n("T3", "Artifact / blob store", 1530, 500, 170, 44, STORE),
        n("T4", "Budget / usage / audit", 900, 500, 160, 44, STORE),

        n("NOTE", "Terminal states: completed / partial / smoke / blocked / failed / needs_review. Cancellation, timeout and retry write terminal events; late results cannot revive a terminal task. Memory writes are best-effort and never change the plan outcome.", 40, 900, 1680, 48, NOTE),
    ]
    edges = [
        e("r1", "I1", "C1", "submit", from_side="right", to_side="left"),
        e("r2", "I2", "C1", "parse", from_side="right", to_side="left"),
        e("r3", "I3", "C2", "metadata", from_side="right", to_side="left"),
        e("r4", "C1", "C5", "bind", from_side="bottom", to_side="top"),
        e("r5", "C2", "C5", "bind", from_side="bottom", to_side="top"),
        e("r6", "C4", "C5", "retrieve", from_side="bottom", to_side="top"),
        e("r7", "C5", "P1", "context", from_side="right", to_side="left"),
        e("r8", "P1", "P2", "classify", from_side="bottom", to_side="top"),
        e("r9", "P2", "P3", "route", from_side="bottom", to_side="top"),
        e("r10", "P3", "P4", "build", from_side="bottom", to_side="top"),
        e("r11", "P4", "P5", "validate", from_side="bottom", to_side="top"),
        e("r12", "P5", "R1", "save revision", from_side="right", to_side="left"),
        e("r13", "R1", "R2", "review?", from_side="bottom", to_side="top"),
        e("r14", "R2", "R3", "needs review", from_side="bottom", to_side="top"),
        e("r15", "R2", "R5", "auto pass", from_side="bottom", to_side="top", dashed=True),
        e("r16", "R3", "R4", "question", from_side="bottom", to_side="top"),
        e("r17", "R4", "R5", "answer", from_side="bottom", to_side="top"),
        e("r18", "R5", "Q1", "execute", from_side="right", to_side="left"),
        e("r19", "Q1", "Q2", "lease", from_side="bottom", to_side="top"),
        e("r20", "Q2", "Q3", "dispatch", from_side="bottom", to_side="top"),
        e("r21", "Q3", "Q4", "important op?", from_side="bottom", to_side="top"),
        e("r22", "Q4", "Q5", "request approval", from_side="bottom", to_side="top"),
        e("r23", "Q5", "A1", "approved", from_side="right", to_side="left"),
        e("r24", "A1", "A2", "resolve", from_side="bottom", to_side="top"),
        e("r25", "A2", "A3", "inject", from_side="bottom", to_side="top"),
        e("r26", "A3", "A4", "invoke", from_side="bottom", to_side="top"),
        e("r27", "A4", "A5", "tool call", from_side="bottom", to_side="top"),
        e("r28", "A5", "X1", "sandbox API", from_side="right", to_side="left"),
        e("r29", "X1", "X2", "proxy", from_side="bottom", to_side="top"),
        e("r30", "X2", "X3", "capabilities", from_side="bottom", to_side="top"),
        e("r31", "X3", "X4", "select", from_side="bottom", to_side="top"),
        e("r32", "X4", "X5", "execute", from_side="bottom", to_side="top"),
        e("r33", "X5", "Z1", "results", from_side="right", to_side="left"),
        e("r34", "Z1", "Z2", "evidence", from_side="bottom", to_side="top"),
        e("r35", "Z2", "Z3", "terminal", from_side="bottom", to_side="top"),
        e("r36", "Z3", "Z4", "events", from_side="bottom", to_side="top"),
        e("r37", "Z4", "Z5", "SSE", from_side="bottom", to_side="top"),
        e("r38", "P5", "T1", "persist", from_side="bottom", to_side="top", dashed=True),
        e("r39", "A3", "T2", "read / write", from_side="bottom", to_side="top", dashed=True),
        e("r40", "Z2", "T3", "artifacts", from_side="bottom", to_side="top", dashed=True),
        e("r41", "Q4", "T4", "budget / audit", from_side="bottom", to_side="top", dashed=True),
    ]
    return {"id": "runtime-flow", "name": "Runtime Flow", "width": 1760, "height": 990, "nodes": nodes, "edges": edges}


def agent_flow():
    nodes = [
        n("T", "Agent Execution Flow — routing, memory, tools, sandbox and evidence", 40, 16, 1680, 40, TITLE),
        n("START", "Scheduler TaskNode\n(type · assigned_to · allowed_tools · inputs)", 660, 80, 440, 58),
        n("ROUTER", "RoutedTaskExecutor\nresolve runner", 660, 172, 440, 52),
        n("PROFILE", "AgentProfile = PromptVersion + ModelProfile\n+ ToolAllowlist + MaxSteps", 660, 258, 440, 56),
        n("MEMORY", "Memory injection: core_memory + retrieved_memory + memory_refs", 660, 336, 440, 46),
        n("DEC", "Agent type?", 770, 414, 220, 80, DECISION),

        n("G-A", "Specialized agents", 40, 540, 1680, 300, GROUP),
        n("AG1", "Chat / General", 60, 578, 380, 38, AGENT),
        n("AG2", "Librarian", 460, 578, 380, 38, AGENT),
        n("AG3", "Benchmark", 860, 578, 380, 38, AGENT),
        n("AG4", "Coder", 1260, 578, 440, 38, AGENT),
        n("AG5", "Research Coding", 60, 626, 380, 38, AGENT),
        n("AG6", "Data", 460, 626, 380, 38, AGENT),
        n("AG7", "AutoResearch", 860, 626, 380, 38, AGENT),
        n("AG8", "Experiment Research", 1260, 626, 440, 38, AGENT),
        n("AG9", "Ablation ToT", 60, 674, 380, 38, AGENT),
        n("AG10", "Paper Debug", 460, 674, 380, 38, AGENT),
        n("AG11", "Visual Verifier", 860, 674, 380, 38, AGENT),
        n("AG12", "Answer", 1260, 674, 440, 38, AGENT),
        n("G-NOTE", "Every agent gets a bounded prompt, a frozen model profile and an explicit tool allowlist. No agent executes user code directly.", 60, 730, 1640, 34, NOTE),

        n("TOOL", "ToolInvoker\nschema · grant · approval · ToolCallRecord", 660, 880, 440, 56, SECURITY),
        n("SB", "Sandbox Client → gateway → executor → provider (docker / native / vm / remote)", 500, 976, 760, 50),
        n("ART", "Artifact builder\n(non-empty · typed · producer-bound)", 660, 1064, 440, 50),
        n("VALID", "Artifact validation\nunique producer / contract", 660, 1152, 440, 50),
        n("STORE", "PlanStore + Event Bus + SSE", 660, 1240, 440, 46, STORE),
        n("TERM", "Task terminal status\ncompleted / failed / canceled", 660, 1324, 440, 46),

        n("G-CLM", "Claim Evidence loop", 40, 880, 380, 260, GROUP),
        n("C1", "Freeze claim rubric", 60, 916, 340, 36),
        n("C2", "Collect evidence inventory", 60, 962, 340, 36),
        n("C3", "LLM adjudication per criterion", 60, 1008, 340, 36),
        n("C4", "Verdict graph + coverage", 60, 1054, 340, 36),

        n("G-AUTO", "AutoResearch loop", 1340, 880, 380, 260, GROUP),
        n("R1", "Freeze ResearchSpec", 1360, 916, 340, 36),
        n("R2", "Run baseline", 1360, 962, 340, 36),
        n("R3", "Propose candidate patch", 1360, 1008, 340, 36),
        n("R4", "Evaluate candidate", 1360, 1054, 340, 36),
        n("R5", "Keep / Reject + rollback", 1360, 1100, 340, 36),
        n("R6", "Hidden holdout validation", 1360, 1146, 340, 36),

        n("NOTE", "Artifacts that are empty, have duplicate producers or violate their contract fail the task.\nSandbox capability gaps return blocked + missing_requirements; visual verification failure is advisory and never fabricates completed.", 440, 1400, 1280, 70, NOTE),
    ]
    edges = [
        e("a1", "START", "ROUTER", "", from_side="bottom", to_side="top"),
        e("a2", "ROUTER", "PROFILE", "resolve", from_side="bottom", to_side="top"),
        e("a3", "PROFILE", "MEMORY", "inject", from_side="bottom", to_side="top"),
        e("a4", "MEMORY", "DEC", "", from_side="bottom", to_side="top"),
        e("a5", "DEC", "G-A", "route", from_side="bottom", to_side="top"),
        e("a6", "G-A", "TOOL", "tool call", from_side="bottom", to_side="top"),
        e("a7", "G-A", "SB", "sandbox call", from_side="bottom", to_side="top"),
        e("a8", "TOOL", "SB", "approved execution", from_side="bottom", to_side="top", dashed=True),
        e("a9", "SB", "ART", "stdout / metrics / files", from_side="bottom", to_side="top"),
        e("a10", "ART", "VALID", "", from_side="bottom", to_side="top"),
        e("a11", "VALID", "STORE", "", from_side="bottom", to_side="top"),
        e("a12", "STORE", "TERM", "", from_side="bottom", to_side="top"),
        e("a13", "AG2", "C1", "claim rubric", from_side="left", to_side="top"),
        e("a14", "C1", "C2", "", from_side="bottom", to_side="top"),
        e("a15", "C2", "C3", "", from_side="bottom", to_side="top"),
        e("a16", "C3", "C4", "", from_side="bottom", to_side="top"),
        e("a17", "C4", "ART", "graph", from_side="right", to_side="left", dashed=True),
        e("a18", "AG7", "R1", "AutoResearch", from_side="right", to_side="top"),
        e("a19", "R1", "R2", "", from_side="bottom", to_side="top"),
        e("a20", "R2", "R3", "", from_side="bottom", to_side="top"),
        e("a21", "R3", "R4", "", from_side="bottom", to_side="top"),
        e("a22", "R4", "R5", "", from_side="bottom", to_side="top"),
        e("a23", "R5", "R3", "reject", from_side="left", to_side="left", dashed=True),
        e("a24", "R5", "R6", "keep", from_side="bottom", to_side="top"),
        e("a25", "R6", "ART", "validation", from_side="left", to_side="right", dashed=True),
    ]
    return {"id": "agent-execution-flow", "name": "Agent Execution Flow", "width": 1760, "height": 1500, "nodes": nodes, "edges": edges}



def memory_system():
    nodes = [
        n("T", "Agent Memory & Paper Wiki — System Architecture", 40, 16, 1680, 40, TITLE),

        n("G-UI", "Interfaces", 40, 90, 320, 380, GROUP),
        n("U1", "Researcher / Desktop user", 60, 130, 280, 42),
        n("U2", "Chat & Plan UI", 60, 190, 280, 42),
        n("U3", "MemoryPanel / PaperWikiPanel", 60, 250, 280, 42),
        n("U4", "Citations & review actions", 60, 310, 280, 42, NOTE),

        n("G-CORE", "Go backend — memory, retrieval, extraction and Paper Wiki", 390, 90, 860, 700, GROUP),
        n("C1", "Memory & Wiki API\n/api/memories · /api/wiki", 410, 130, 260, 46),
        n("C2", "Retrieval pipeline\nBM25 + entity + RRF/MMR", 410, 210, 260, 46),
        n("C3", "Budget & render\ncore / retrieved / wiki", 410, 290, 260, 46),
        n("C4", "Prompt assembly\ncore_memory · retrieved_memory · wiki_context", 410, 370, 260, 46),
        n("C5", "ChatAgent / RoutedTaskExecutor\ninject + record", 410, 450, 260, 46),
        n("C6", "Deterministic extractors\nRecordPlan / Task / Trial / Document", 720, 130, 250, 46),
        n("C7", "LLM summarizer (W3)\nTLDR · method · limitation", 720, 210, 250, 46),
        n("C8", "Link builder (W2/W2.5)\nentities · OpenAlex · PDF refs", 720, 290, 250, 46),
        n("C9", "Consolidator\nADD/UPDATE/DELETE/NOOP · TTL", 720, 370, 250, 46),
        n("NOTE1", "Memory is data, never instructions. Every write is owner-scoped, redacted, hidden-benchmark filtered and injection-scanned.", 410, 530, 800, 48, NOTE),

        n("G-STORE", "Owner-scoped persistence", 1290, 90, 430, 700, GROUP),
        n("S1", "Memory FileStore\nmemory.wal.jsonl + memory.snapshot.json + lock", 1310, 130, 390, 58, STORE),
        n("S2", "ChatSessionStore + per-owner settings\nturns · summary · switches", 1310, 220, 390, 52, STORE),
        n("S3", "Paper Wiki store\npapers.jsonl + papers.snapshot.json + aliases.json", 1310, 310, 390, 58, STORE),
        n("S4", "DocumentStore + Literature library\nPDF text/sections · DOI/arXiv records", 1310, 400, 390, 58, STORE),
        n("NOTE2", "PaperPage is a rebuildable projection; Point and Link are memory.Record views. Deleting data/wiki never loses points, links or review decisions.", 1310, 500, 390, 80, NOTE),
    ]
    edges = [
        e("m1", "U1", "U2", "chat / task", from_side="bottom", to_side="top"),
        e("m2", "U2", "C1", "REST / SSE", from_side="right", to_side="left"),
        e("m3", "U3", "C1", "CRUD / review", from_side="right", to_side="left"),
        e("m4", "C1", "C2", "retrieve", from_side="bottom", to_side="top"),
        e("m5", "C2", "C3", "scored records", from_side="bottom", to_side="top"),
        e("m6", "C3", "C4", "bounded context", from_side="bottom", to_side="top"),
        e("m7", "C4", "C5", "inject", from_side="bottom", to_side="top"),
        e("m8", "C5", "C6", "record plan/task/trial", from_side="right", to_side="left"),
        e("m9", "C5", "C7", "paper_parse", from_side="right", to_side="left"),
        e("m10", "C5", "C8", "build links", from_side="right", to_side="left"),
        e("m11", "C5", "C9", "background", from_side="right", to_side="left"),
        e("m12", "C2", "S1", "records + index", from_side="right", to_side="left"),
        e("m13", "C6", "S1", "Put", from_side="right", to_side="left"),
        e("m14", "C7", "S1", "wiki_tldr / points", from_side="right", to_side="left"),
        e("m15", "C8", "S1", "wiki_link records", from_side="right", to_side="left"),
        e("m16", "C9", "S1", "merge / TTL", from_side="right", to_side="left"),
        e("m17", "C8", "S3", "PaperPage / links", from_side="right", to_side="left"),
        e("m18", "C1", "S3", "papers / points", from_side="right", to_side="left"),
        e("m19", "S3", "S1", "rebuild", dashed=True, from_side="left", to_side="left"),
        e("m20", "C8", "S4", "PDF refs / DOI", from_side="right", to_side="left"),
    ]
    return {"id": "memory-system", "name": "Memory System Architecture", "width": 1760, "height": 920, "nodes": nodes, "edges": edges}

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
        style = edge["style"]
        if edge.get("fromSide"):
            style += "exitX=0.5;exitY=1;exitDx=0;exitDy=0;" if edge["fromSide"] == "bottom" else ""
        if edge.get("toSide"):
            style += "entryX=0.5;entryY=0;entryDx=0;entryDy=0;" if edge["toSide"] == "top" else ""
        cells.append(
            '<mxCell id="%s" value="%s" style="%s" edge="1" parent="1" source="%s" target="%s">'
            '<mxGeometry relative="1" as="geometry" /></mxCell>'
            % (html.escape(edge["id"], quote=True), html.escape(edge["label"], quote=True),
               html.escape(style, quote=True), html.escape(edge["from"], quote=True), html.escape(edge["to"], quote=True))
        )
    return (
        '<mxfile host="app.diagrams.net" modified="2026-11-02T00:00:00.000Z" agent="research-assistant" version="24.7.17" type="device">\n'
        '  <diagram id="%s" name="%s">\n'
        '    <mxGraphModel dx="1760" dy="1000" grid="1" gridSize="10" guides="1" tooltips="1" connect="1" arrows="1" fold="1" page="1" pageScale="1" pageWidth="%s" pageHeight="%s" math="0" shadow="0">\n'
        '      <root>\n        %s\n      </root>\n    </mxGraphModel>\n  </diagram>\n</mxfile>\n'
        % (diagram["id"], html.escape(diagram["name"], quote=True), diagram["width"], diagram["height"], "\n        ".join(cells))
    )


def color(style, key, fallback):
    import re
    match = re.search(key + r"=(#[0-9a-fA-F]{6})", style)
    return match.group(1) if match else fallback


def anchor(node, side):
    x, y, w, h = node["x"], node["y"], node["w"], node["h"]
    if side == "left":
        return (x, y + h / 2)
    if side == "right":
        return (x + w, y + h / 2)
    if side == "top":
        return (x + w / 2, y)
    if side == "bottom":
        return (x + w / 2, y + h)
    return (x + w / 2, y + h / 2)


def choose_sides(a, b, edge):
    fs, ts = edge.get("fromSide"), edge.get("toSide")
    if fs and ts:
        return fs, ts
    ax, ay = a["x"] + a["w"] / 2, a["y"] + a["h"] / 2
    bx, by = b["x"] + b["w"] / 2, b["y"] + b["h"] / 2
    dx, dy = bx - ax, by - ay
    if abs(dx) >= abs(dy):
        return ("right", "left") if dx >= 0 else ("left", "right")
    return ("bottom", "top") if dy >= 0 else ("top", "bottom")


def _inflate(node, pad=4):
    return (node["x"] - pad, node["y"] - pad, node["x"] + node["w"] + pad, node["y"] + node["h"] + pad)


def _point_blocked(x, y, obstacles):
    for x1, y1, x2, y2 in obstacles:
        if x1 <= x <= x2 and y1 <= y <= y2:
            return True
    return False


def _simplify(points):
    if len(points) < 3:
        return points
    out = [points[0]]
    for p in points[1:]:
        if len(out) >= 2:
            a, b = out[-2], out[-1]
            if (abs(a[0] - b[0]) < 1e-6 and abs(b[0] - p[0]) < 1e-6) or (abs(a[1] - b[1]) < 1e-6 and abs(b[1] - p[1]) < 1e-6):
                out[-1] = p
                continue
        out.append(p)
    return out


def route_points(a, b, edge, nodes=None, width=1760, height=1500):
    fs, ts = choose_sides(a, b, edge)
    p1 = anchor(a, fs)
    p2 = anchor(b, ts)
    if not nodes:
        return [p1, p2]
    # A* on a 10px grid with obstacle inflation and turn penalties.
    step = 10
    obstacles = [_inflate(n) for n in nodes if n["id"] not in (a["id"], b["id"]) and n["style"] not in (GROUP, TITLE)]
    gw = int(width / step) + 2
    gh = int(height / step) + 2
    def cell(p):
        return (max(0, min(gw - 1, int(round(p[0] / step)))), max(0, min(gh - 1, int(round(p[1] / step)))))
    start, goal = cell(p1), cell(p2)
    if _point_blocked(start[0] * step, start[1] * step, obstacles):
        start = (start[0], max(0, start[1] - 1))
    if _point_blocked(goal[0] * step, goal[1] * step, obstacles):
        goal = (goal[0], max(0, goal[1] - 1))
    import heapq
    dirs = [(1, 0), (-1, 0), (0, 1), (0, -1)]
    best = {}
    pq = [(0, 0, start[0], start[1], -1)]
    parent = {}
    found = None
    expansions = 0
    while pq and expansions < 120000:
        cost, g, x, y, d = heapq.heappop(pq)
        if (x, y, d) in best and best[(x, y, d)] < g:
            continue
        if (x, y) == goal:
            found = (x, y, d)
            break
        expansions += 1
        for nd, (dx, dy) in enumerate(dirs):
            nx, ny = x + dx, y + dy
            if nx < 0 or ny < 0 or nx >= gw or ny >= gh:
                continue
            if _point_blocked(nx * step, ny * step, obstacles):
                continue
            ng = g + step + (3 if d != -1 and d != nd else 0)
            state = (nx, ny, nd)
            if state in best and best[state] <= ng:
                continue
            best[state] = ng
            parent[state] = (x, y, d)
            h = (abs(nx - goal[0]) + abs(ny - goal[1])) * step
            heapq.heappush(pq, (ng + h, ng, nx, ny, nd))
    if not found:
        return [p1, p2]
    path = []
    state = found
    while state in parent:
        x, y, _ = state
        path.append((x * step, y * step))
        state = parent[state]
    path.append((start[0] * step, start[1] * step))
    path.reverse()
    pts = [p1] + path + [p2]
    return _simplify(pts)


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
        parts.append('<rect data-node-id="%s" x="%d" y="%d" width="%d" height="%d" fill="%s" stroke="%s" stroke-width="1.2"/>' % (node["id"], node["x"], node["y"], node["w"], node["h"], fill, stroke))
        parts.append('<text x="%d" y="%d" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="14" font-weight="700" fill="#343a40">%s</text>' % (node["x"] + 12, node["y"] + 22, html.escape(node["label"])))

    for edge in diagram["edges"]:
        a, b = nodes.get(edge["from"]), nodes.get(edge["to"])
        if not a or not b:
            continue
        pts = route_points(a, b, edge, list(nodes.values()), width, height)
        dash = ' stroke-dasharray="6 4"' if "dashed=1" in edge["style"] else ""
        d = " ".join(("%.1f,%.1f" % p) for p in pts)
        parts.append('<polyline points="%s" fill="none" stroke="#495057" stroke-width="1.3"%s marker-end="url(#arrow)"/>' % (d, dash))
        if edge["label"]:
            # place label on the longest segment
            best = max(zip(pts, pts[1:]), key=lambda seg: abs(seg[0][0] - seg[1][0]) + abs(seg[0][1] - seg[1][1]))
            mx = (best[0][0] + best[1][0]) / 2
            my = (best[0][1] + best[1][1]) / 2
            parts.append('<text x="%.1f" y="%.1f" text-anchor="middle" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="10.5" fill="#495057" paint-order="stroke" stroke="#ffffff" stroke-width="4">%s</text>' % (mx, my - 4, html.escape(edge["label"])))

    for node in others:
        fill = color(node["style"], "fillColor", "#dae8fc")
        stroke = color(node["style"], "strokeColor", "#6c8ebf")
        rx = 8 if "rhombus" not in node["style"] else 0
        if "rhombus" in node["style"]:
            cx, cy = node["x"] + node["w"] / 2, node["y"] + node["h"] / 2
            points = "%d,%d %d,%d %d,%d %d,%d" % (cx, node["y"], node["x"] + node["w"], cy, cx, node["y"] + node["h"], node["x"], cy)
            parts.append('<polygon data-node-id="%s" points="%s" fill="%s" stroke="%s" stroke-width="1.2"/>' % (node["id"], points, fill, stroke))
        else:
            parts.append('<rect data-node-id="%s" x="%d" y="%d" width="%d" height="%d" rx="%d" fill="%s" stroke="%s" stroke-width="1.2"/>' % (node["id"], node["x"], node["y"], node["w"], node["h"], rx, fill, stroke))
        lines = node["label"].split("\n")
        if node["style"] == TITLE:
            parts.append('<text data-node-id="%s" x="%d" y="%d" text-anchor="middle" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="20" font-weight="700" fill="#212529">%s</text>' % (node["id"], node["x"] + node["w"] / 2, node["y"] + node["h"] / 2 + 7, html.escape(node["label"])))
            continue
        font = 12
        start_y = node["y"] + node["h"] / 2 - (len(lines) - 1) * 7
        for idx, line in enumerate(lines):
            parts.append('<text data-node-id="%s" x="%d" y="%.1f" text-anchor="middle" font-family="Segoe UI, Microsoft YaHei, sans-serif" font-size="%d" fill="#212529">%s</text>' % (node["id"], node["x"] + node["w"] / 2, start_y + idx * 15, font, html.escape(line)))
    parts.append('</svg>\n')
    return "\n".join(parts)


def main():
    for factory in (architecture, runtime_flow, agent_flow, memory_system):
        diagram = factory()
        (OUT / (diagram["id"] + ".drawio")).write_text(drawio_xml(diagram), encoding="utf-8", newline="\n")
        (OUT / (diagram["id"] + ".svg")).write_text(svg(diagram), encoding="utf-8", newline="\n")
        print("wrote", diagram["id"] + ".drawio", diagram["id"] + ".svg")


if __name__ == "__main__":
    main()
