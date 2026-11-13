"""README architecture views, sharing geometry between draw.io and SVG.

All paths are repository-relative evidence references. Nodes, labels, ports,
and explicit orthogonal waypoints remain editable in diagrams.net.
"""
from __future__ import annotations

import html
from xml.etree import ElementTree as ET


PALETTE = {
    "service": ("#edf4ff", "#92acd1"),
    "agent": ("#edf7f0", "#8fbaa0"),
    "knowledge": ("#f3effb", "#b09bcf"),
    "state": ("#fff5e6", "#d7b77d"),
    "security": ("#fff0ed", "#d3a097"),
    "group": ("#f8fafc", "#cbd5e1"),
}
FONT = "Microsoft YaHei, Segoe UI, sans-serif"


def node(key, title, detail, x, y, w, h, tone="service", source=""):
    return dict(id=key, title=title, detail=detail, x=x, y=y, w=w, h=h,
                tone=tone, source=source)


def group(key, title, x, y, w, h):
    return node(key, title, "", x, y, w, h, "group")


def edge(key, source, target, start="bottom", end="top", via=(), dashed=False):
    return dict(id=key, source=source, target=target, start=start, end=end,
                via=list(via), dashed=dashed)


def diagram(key, title, subtitle, height, nodes, edges, note):
    return dict(id=key, name=title, title=title, subtitle=subtitle,
                width=1280, height=height, nodes=nodes, edges=edges,
                note=note, format="overview")


def architecture():
    nodes = [
        group("clients", "01  访问入口 / CLIENTS", 32, 112, 1216, 138),
        node("web", "Web 工作台", "React · DAG · PDF · 文献 · 审批", 56, 168, 352, 60,
             source="research-assistant/frontend/src"),
        node("desktop", "Windows 桌面客户端", "MyGo · supervisor · loopback proxy", 464, 168, 352, 60,
             source="desktop/shell; desktop/supervisor; desktop/proxy"),
        node("api-client", "API / CLI", "REST · 上传 · SSE 事件订阅", 872, 168, 352, 60,
             source="research-assistant/backend/internal/api"),
        group("backend", "02  Go 后端 / ORCHESTRATION", 32, 310, 880, 454),
        node("api", "API 接入与资料解析", "认证 · CORS · 上传 · 文档 · 文献 · SSE", 56, 366, 832, 60,
             source="research-assistant/backend/internal/api"),
        node("planner", "意图与任务规划", "Intent / L0–L4 / DAG 校验", 56, 462, 248, 72,
             source="research-assistant/backend/internal/planner"),
        node("review", "版本与计划审阅", "Revision · Review Gate", 348, 462, 248, 72,
             source="research-assistant/backend/internal/revision"),
        node("scheduler", "调度与执行治理", "租约 · 重试 · 审批 · 预算", 640, 462, 248, 72,
             source="research-assistant/backend/internal/scheduler"),
        node("context", "上下文与记忆", "模型容量 · core / retrieved · Wiki", 56, 582, 384, 72, "knowledge",
             "research-assistant/backend/internal/contextwindow; research-assistant/backend/internal/memory/budget.go"),
        node("router", "RoutedTaskExecutor", "输入绑定 · Agent / Profile 路由", 488, 582, 400, 72, "agent",
             "research-assistant/backend/internal/scheduler/executor.go"),
        node("agents", "专业 Agent + 受控工具", "研究 · 编码 · 评测 · 验证  /  native · HTTP · MCP", 56, 690, 832, 60, "agent",
             "research-assistant/backend/internal/agent; research-assistant/backend/internal/tools"),
        group("resources", "03  模型与状态 / RESOURCES", 944, 310, 304, 454),
        node("llm", "模型服务", "运行期 ModelProfile\n兼容 API / 能力声明", 968, 366, 256, 80, "knowledge",
             "research-assistant/backend/internal/modelresolver"),
        node("memory", "研究知识与资料", "Memory · Paper Wiki\n文档 · 文献库", 968, 474, 256, 80, "knowledge",
             "research-assistant/backend/internal/memory; research-assistant/backend/internal/wiki"),
        node("stores", "持久化状态", "计划 · revision · 预算\nusage · audit · 文件存储", 968, 582, 256, 80, "state",
             "research-assistant/backend/internal/store; research-assistant/backend/internal/budget"),
        node("optimizer", "研究优化器（可选）", "Python · 候选排序 · SQLite", 968, 690, 256, 60, "knowledge",
             "research-assistant/research-optimizer"),
        group("sandbox", "04  沙箱执行与结果 / EXECUTION & EVIDENCE", 32, 824, 1216, 156),
        node("gateway", "Sandbox gateway", "鉴权与转发 · 无 Docker socket", 56, 884, 256, 72, "security",
             "research-assistant/docker-sandbox/gateway.go"),
        node("executor", "Sandbox executor", "执行面 · 资源与网络限制", 356, 884, 256, 72, "security",
             "research-assistant/docker-sandbox/internal/engine"),
        node("provider", "Provider", "docker / native / vm / remote", 656, 884, 256, 72,
             source="research-assistant/docker-sandbox/internal/engine"),
        node("artifacts", "产物与验收", "Artifact · 环境清单 · 证据图", 956, 884, 268, 72, "state",
             "research-assistant/backend/internal/planner/semantic_validation.go"),
    ]
    edges = [
        edge("client-api", "clients", "api", via=((640, 280), (472, 280))),
        edge("api-plan", "api", "planner", via=((472, 444), (180, 444))),
        edge("plan-review", "planner", "review", "right", "left"),
        edge("review-schedule", "review", "scheduler", "right", "left"),
        edge("schedule-route", "scheduler", "router", via=((764, 558), (688, 558))),
        edge("context-route", "context", "router", "right", "left"),
        edge("route-agents", "router", "agents", via=((688, 674), (472, 674))),
        edge("llm-route", "llm", "router", "left", "right", ((928, 406), (928, 618)), True),
        edge("agent-execution", "agents", "sandbox", via=((472, 794), (640, 794))),
        edge("gateway-executor", "gateway", "executor", "right", "left"),
        edge("executor-provider", "executor", "provider", "right", "left"),
        edge("provider-artifacts", "provider", "artifacts", "right", "left"),
    ]
    return diagram("architecture", "系统架构 / SYSTEM ARCHITECTURE",
                   "研究目标 → 可审阅计划 → 隔离执行 → 可验证结果", 1072, nodes, edges,
                   "事件总线 / SSE 回传工作台；PlanOutcome 区分完成、部分完成、smoke 与阻断。")


def memory_system():
    nodes = [
        group("write", "01  写入与治理 / WRITE", 32, 112, 368, 614),
        group("persist", "02  状态与投影 / STATE", 456, 112, 368, 614),
        group("read", "03  检索与上下文 / READ", 880, 112, 368, 614),
        node("extract", "事件与确定性抽取", "会话 · Plan · Task · Trial · Document", 56, 180, 320, 80,
             source="research-assistant/backend/internal/memory/extract.go"),
        node("validate", "记录校验与作用域", "owner / scope · 脱敏\n隐藏评测过滤 · 注入扫描", 56, 312, 320, 88, "security",
             "research-assistant/backend/internal/memory/redact.go; research-assistant/backend/internal/memory/types.go"),
        node("consolidate", "条件案例与维护", "来源 · 环境 · 协议 · 成败条件\nTTL / 归档 · 不自动泛化", 56, 454, 320, 88,
             source="research-assistant/backend/internal/memory/consolidate.go"),
        node("human", "人工审阅与修订", "pending_review · pin · supersede\nLLM / 归纳候选保留待审状态", 56, 596, 320, 88, "security",
             "research-assistant/backend/internal/api/memories.go"),
        node("session", "会话与活动窗口", "ChatSession · contextwindow\n历史 · 摘要 · 私有分支", 480, 180, 320, 80, "state",
             "research-assistant/backend/internal/memory/chat_store.go; research-assistant/backend/internal/contextwindow/store.go"),
        node("memory", "Memory FileStore", "WAL + snapshot\nBM25 / 实体索引", 480, 312, 320, 88, "state",
             "research-assistant/backend/internal/memory/store.go"),
        node("wiki", "Paper Wiki", "PaperPage 为可重建投影\nPoint / Link 来源于 memory.Record", 480, 454, 320, 88, "knowledge",
             "research-assistant/backend/internal/wiki"),
        node("documents", "文档与文献库", "页码 · 章节 · quote · DOI / arXiv\n原文引用与论文身份", 480, 596, 320, 88, "state",
             "research-assistant/backend/internal/documents; research-assistant/backend/internal/literature"),
        node("retrieve", "范围过滤与召回", "BM25 / entity → RRF / MMR\n信任 · 时效 · 环境与协议兼容", 904, 180, 320, 80, "knowledge",
             "research-assistant/backend/internal/memory/retrieve.go"),
        node("budget", "共享上下文预算", "必需输入 → core → 动态池\n召回 / Wiki / 文档 / memory 共用", 904, 312, 320, 88, "knowledge",
             "research-assistant/backend/internal/memory/budget.go; research-assistant/backend/internal/api/chat_runtime.go"),
        node("material", "带来源的资料块", "id · source · trust · version\nverbatim / retrievable 保留策略", 904, 454, 320, 88, "knowledge",
             "research-assistant/backend/internal/contextwindow/material.go"),
        node("preflight", "模型调用与压缩检查", "按实际容量预检 · 原文摘要校验\n必需原文超限时显式失败", 904, 596, 320, 88,
             source="research-assistant/backend/internal/modelresolver/runtime.go; research-assistant/backend/internal/contextwindow/summary.go"),
        group("output", "04  统一输出 / CONTEXT & CITATIONS", 32, 790, 1216, 130),
        node("consumer", "Chat API / RoutedTaskExecutor → modelresolver → 模型", "来源引用 · context_budget_report（检索分配） · context_report（最终预检）", 56, 844, 1168, 56,
             source="research-assistant/backend/internal/api/chat_runtime.go; research-assistant/backend/internal/scheduler/executor.go"),
    ]
    edges = [
        edge("extract-validation", "extract", "validate"),
        edge("validated-write", "validate", "memory", "right", "left"),
        edge("stored-cases", "memory", "consolidate", "left", "right", ((428, 356), (428, 498)), True),
        edge("case-review", "consolidate", "human"),
        edge("review-write", "human", "memory", "right", "left", ((412, 640), (412, 376), (464, 376), (464, 356)), True),
        edge("store-recall", "memory", "retrieve", "right", "left", ((848, 356), (848, 220))),
        edge("session-recall", "session", "retrieve", "right", "left", dashed=True),
        edge("wiki-projection", "memory", "wiki", dashed=True),
        edge("document-wiki", "documents", "wiki", "top", "bottom", dashed=True),
        edge("wiki-budget", "wiki", "budget", "right", "bottom", ((848, 498), (848, 430), (1064, 430)), True),
        edge("document-budget", "documents", "budget", "right", "bottom", ((864, 640), (864, 430), (1064, 430)), True),
        edge("retrieve-budget", "retrieve", "budget"),
        edge("budget-material", "budget", "material"),
        edge("material-check", "material", "preflight"),
        edge("checked-output", "preflight", "output", via=((1064, 756), (640, 756))),
    ]
    return diagram("memory-system", "记忆架构 / MEMORY & CONTEXT",
                   "可追溯的研究经验 · 按作用域检索 · 按模型容量注入", 1012, nodes, edges,
                   "资料以普通数据块传入；关闭或 shadow 可回滚。原文校验不等于开放语义真实性验证。")


def agent_flow():
    nodes = [
        group("prepare", "01  任务准备 / DISPATCH", 32, 112, 1216, 148),
        node("task", "Scheduler / TaskNode", "type · assigned_to · inputs · contract", 56, 172, 352, 64,
             source="research-assistant/backend/internal/scheduler/scheduler.go"),
        node("binding", "输入绑定与上下文", "InputBindings · 记忆 · Wiki · 模型预算", 464, 172, 352, 64, "knowledge",
             "research-assistant/backend/internal/scheduler/executor.go"),
        node("router", "RoutedTaskExecutor", "内置 runner 或 profile:<id>", 872, 172, 352, 64, "agent",
             "research-assistant/backend/internal/scheduler/executor.go"),
        group("agents", "02  专业 Agent 与可扩展执行器 / AGENTS", 32, 320, 1216, 340),
        node("research", "资料与回答", "Chat / General\nLibrarian · Answer\n检索 · 论文解析 · 回答与引用", 56, 380, 352, 142, "agent",
             "research-assistant/backend/internal/agent/chat.go; research-assistant/backend/internal/agent/librarian.go; research-assistant/backend/internal/agent/answer.go"),
        node("coding", "编码与研究", "Coder · Research Coding\nAutoResearch / Experiment / Paper Debug\n运行时 · 补丁 · 实验循环", 464, 380, 352, 142, "agent",
             "research-assistant/backend/internal/agent/research_coding_agent.go; research-assistant/backend/internal/agent/autoresearch.go"),
        node("evaluation", "评测与验证", "Benchmark · Data\nAblation ToT · Visual Verifier\n冻结契约 · 指标重算 · 证据验收", 872, 380, 352, 142, "agent",
             "research-assistant/backend/internal/agent/benchmark_agent.go; research-assistant/backend/internal/agent/data.go; research-assistant/backend/internal/agent/visual_verifier.go"),
        node("profile", "自定义 AgentProfile", "PromptVersion + ModelProfile + ToolAllowlist + MaxSteps\n可选 tool_loop：原生工具调用 · 检查点恢复 · 显式步数上限", 56, 562, 1168, 84, "knowledge",
             "research-assistant/backend/internal/agentprofile/runner.go; research-assistant/backend/internal/agentprofile"),
        group("runtime", "03  模型、工具与隔离执行 / RUNTIME", 32, 724, 1216, 174),
        node("model", "模型调用 / modelresolver", "动态 Profile · 上下文预检\n能力声明 · 输出与费用约束", 56, 786, 352, 88, "knowledge",
             "research-assistant/backend/internal/modelresolver"),
        node("tools", "ToolInvoker", "Schema · allowlist / grant · 审批\nnative / HTTP / MCP · 调用审计", 464, 786, 352, 88, "security",
             "research-assistant/backend/internal/tools"),
        node("sandbox", "Sandbox Client", "gateway → executor → Provider\n真实代码执行 · 日志 · 文件", 872, 786, 352, 88, "security",
             "research-assistant/backend/internal/sandbox; research-assistant/docker-sandbox/internal/engine"),
        group("results", "04  结果与回传 / ACCEPTANCE", 32, 958, 1216, 150),
        node("artifacts", "产物与数值校验", "类型 · producer · 契约 · 执行来源", 56, 1018, 352, 66, "state",
             "research-assistant/backend/internal/scheduler"),
        node("outcome", "PlanFinalizer / Outcome", "完成 / partial / smoke / blocked", 464, 1018, 352, 66, "state",
             "research-assistant/backend/internal/planner/semantic_validation.go"),
        node("events", "状态持久化与工作台", "Plan Store · MemorySink · SSE", 872, 1018, 352, 66, "state",
             "research-assistant/backend/internal/scheduler; research-assistant/backend/internal/api"),
    ]
    edges = [
        edge("task-binding", "task", "binding", "right", "left"),
        edge("binding-router", "binding", "router", "right", "left"),
        edge("dispatch-agent", "router", "agents", via=((1048, 292), (640, 292))),
        edge("agent-runtime", "agents", "runtime"),
        edge("model-tool", "model", "tools", "right", "left", dashed=True),
        edge("tool-sandbox", "tools", "sandbox", "right", "left", dashed=True),
        edge("runtime-results", "runtime", "results"),
        edge("artifact-outcome", "artifacts", "outcome", "right", "left"),
        edge("outcome-events", "outcome", "events", "right", "left"),
    ]
    return diagram("agent-execution-flow", "Agent 架构 / AGENT EXECUTION",
                   "专业分工 · 受控工具 · 真实执行 · 以证据决定结果", 1200, nodes, edges,
                   "内置 runner 承载各领域循环；运行期审批、分类恢复与共享恢复额度贯穿执行过程。")


def anchor(node_data, side):
    x, y, w, h = (node_data[k] for k in ("x", "y", "w", "h"))
    return {"top": (x+w/2, y), "bottom": (x+w/2, y+h),
            "left": (x, y+h/2), "right": (x+w, y+h/2)}[side]


def points(diagram_data, connection):
    by_id = {item["id"]: item for item in diagram_data["nodes"]}
    return [anchor(by_id[connection["source"]], connection["start"]),
            *connection["via"], anchor(by_id[connection["target"]], connection["end"])]


def label(item):
    title = "<b>" + html.escape(item["title"]) + "</b>"
    if not item["detail"]:
        return title
    return title + '<br><font style="font-size:16px">' + html.escape(item["detail"]).replace("\n", "<br>") + "</font>"


def drawio(diagram_data):
    root = ET.Element("mxfile", host="app.diagrams.net", agent="research-assistant",
                      version="24.7.17", type="device")
    page = ET.SubElement(root, "diagram", id=diagram_data["id"], name=diagram_data["name"])
    model = ET.SubElement(page, "mxGraphModel", grid="1", gridSize="8", guides="1",
                          tooltips="1", connect="1", arrows="1", fold="1", page="1",
                          pageScale="1", pageWidth=str(diagram_data["width"]),
                          pageHeight=str(diagram_data["height"]), background="#ffffff",
                          math="0", shadow="0")
    cells = ET.SubElement(model, "root")
    ET.SubElement(cells, "mxCell", id="0")
    ET.SubElement(cells, "mxCell", id="1", parent="0")

    def vertex(key, value, style, x, y, w, h, source=""):
        cell = ET.SubElement(cells, "mxCell", id=key, parent="1", vertex="1",
                             value=value, style=style, tooltip=source)
        ET.SubElement(cell, "mxGeometry", x=str(x), y=str(y), width=str(w),
                      height=str(h), **{"as": "geometry"})

    base = "whiteSpace=wrap;html=1;fontFamily=Microsoft YaHei;fontColor=#1e293b;"
    vertex("title", html.escape(diagram_data["title"]), base+"text;align=left;fontSize=28;fontStyle=1;",
           32, 24, 1216, 36)
    vertex("subtitle", html.escape(diagram_data["subtitle"]), base+"text;align=left;fontSize=18;fontColor=#64748b;",
           32, 68, 1216, 28)
    for item in diagram_data["nodes"]:
        fill, stroke = PALETTE[item["tone"]]
        style = base+f"rounded=1;arcSize=10;fillColor={fill};strokeColor={stroke};strokeWidth=1.2;fontSize=18;spacing=10;"
        if item["tone"] == "group":
            style += "align=left;verticalAlign=top;spacingLeft=22;spacingTop=14;fontSize=20;"
        vertex(item["id"], label(item), style, item["x"], item["y"], item["w"], item["h"], item["source"])
    fractions = {"top": (0.5, 0), "bottom": (0.5, 1), "left": (0, 0.5), "right": (1, 0.5)}
    for connection in diagram_data["edges"]:
        sx, sy = fractions[connection["start"]]
        tx, ty = fractions[connection["end"]]
        style = (f"html=1;noEdgeStyle=1;rounded=0;endArrow=block;endFill=1;strokeColor=#64748b;strokeWidth=1.5;"
                 f"exitX={sx};exitY={sy};entryX={tx};entryY={ty};exitPerimeter=0;entryPerimeter=0;")
        if connection["dashed"]:
            style += "dashed=1;dashPattern=6 4;"
        cell = ET.SubElement(cells, "mxCell", id=connection["id"], edge="1", parent="1",
                             source=connection["source"], target=connection["target"], style=style)
        geometry = ET.SubElement(cell, "mxGeometry", relative="1", **{"as": "geometry"})
        if connection["via"]:
            waypoints = ET.SubElement(geometry, "Array", **{"as": "points"})
            for x, y in connection["via"]:
                ET.SubElement(waypoints, "mxPoint", x=str(x), y=str(y))
    footer = diagram_data["height"]-58
    vertex("note", html.escape(diagram_data["note"]), base+"text;align=left;fontSize=16;fontColor=#475569;",
           32, footer, 1216, 40)
    ET.indent(root, space="  ")
    return ET.tostring(root, encoding="unicode")+"\n"


def svg(diagram_data):
    ns = "http://www.w3.org/2000/svg"
    ET.register_namespace("", ns)

    def tag(parent, kind, **attrs):
        return ET.SubElement(parent, "{"+ns+"}"+kind, {k.replace("_", "-"): str(v) for k, v in attrs.items()})

    root = ET.Element("{"+ns+"}svg", width=str(diagram_data["width"]), height=str(diagram_data["height"]),
                      viewBox=f'0 0 {diagram_data["width"]} {diagram_data["height"]}',
                      role="img", **{"aria-labelledby": "diagram-title diagram-description"})
    tag(root, "title", id="diagram-title").text = diagram_data["title"]
    tag(root, "desc", id="diagram-description").text = diagram_data["subtitle"]+"。"+diagram_data["note"]
    defs = tag(root, "defs")
    marker = tag(defs, "marker", id="arrow", markerWidth=8, markerHeight=8,
                 refX=7, refY=4, orient="auto", markerUnits="userSpaceOnUse")
    tag(marker, "path", d="M0 0 L8 4 L0 8 Z", fill="#64748b")
    tag(root, "rect", width=diagram_data["width"], height=diagram_data["height"], fill="#ffffff")

    def text(parent, value, x, y, size=18, weight=400, color="#1e293b", align="middle", **attrs):
        element = tag(parent, "text", x=x, y=y, font_family=FONT, font_size=size,
                      font_weight=weight, fill=color, text_anchor=align,
                      dominant_baseline="middle", **attrs)
        element.text = value
        return element

    text(root, diagram_data["title"], 32, 44, 28, 700, align="start")
    text(root, diagram_data["subtitle"], 32, 82, 18, color="#64748b", align="start")

    def box(item):
        fill, stroke = PALETTE[item["tone"]]
        container = tag(root, "g", **{"data-node-id": item["id"], "data-source": item["source"]})
        if item["source"]:
            tag(container, "title").text = item["source"]
        tag(container, "rect", x=item["x"], y=item["y"], width=item["w"], height=item["h"],
            rx=8, fill=fill, stroke=stroke, stroke_width=1.2)
        return container

    for item in diagram_data["nodes"]:
        if item["tone"] == "group":
            container = box(item)
            text(container, item["title"], item["x"]+22, item["y"]+28, 20, 700, align="start")
    for connection in diagram_data["edges"]:
        attrs = dict(points=" ".join(f"{x:g},{y:g}" for x, y in points(diagram_data, connection)),
                     fill="none", stroke="#64748b", stroke_width=1.5, marker_end="url(#arrow)")
        if connection["dashed"]:
            attrs["stroke_dasharray"] = "6 4"
        tag(root, "polyline", **attrs, **{"data-edge-id": connection["id"]})
    for item in diagram_data["nodes"]:
        if item["tone"] == "group":
            continue
        container = box(item)
        details = item["detail"].split("\n") if item["detail"] else []
        cx, cy = item["x"]+item["w"]/2, item["y"]+item["h"]/2
        first = cy+3-(len(details)*23)/2
        text(container, item["title"], cx, first, 18, 700)
        for index, line in enumerate(details, 1):
            text(container, line, cx, first+index*23, 16, color="#475569")
    footer = diagram_data["height"]-58
    tag(root, "line", x1=32, y1=footer-10, x2=1248, y2=footer-10, stroke="#e2e8f0")
    text(root, diagram_data["note"], 32, footer+18, 16, color="#475569", align="start")
    ET.indent(root, space="  ")
    return ET.tostring(root, encoding="unicode")+"\n"
