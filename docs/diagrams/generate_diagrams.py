#!/usr/bin/env python3
"""Generate editable draw.io diagrams and matching SVG previews.

README overviews use overview_diagrams.py for shared explicit geometry.
The detailed runtime flow retains its existing declarative layout below.
Pass diagram IDs to regenerate selected views, or omit them for all four.
"""
from __future__ import annotations

import argparse
import html
from pathlib import Path

if __package__:
    from .overview_diagrams import architecture, agent_flow, memory_system
    from .overview_diagrams import drawio as overview_drawio, svg as overview_svg
else:
    from overview_diagrams import architecture, agent_flow, memory_system
    from overview_diagrams import drawio as overview_drawio, svg as overview_svg

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


def drawio_xml(diagram):
    if diagram.get("format") == "overview":
        return overview_drawio(diagram)
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
    if diagram.get("format") == "overview":
        return overview_svg(diagram)
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
    factories = {"architecture": architecture, "runtime-flow": runtime_flow,
                 "agent-execution-flow": agent_flow, "memory-system": memory_system}
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("diagrams", nargs="*", metavar="DIAGRAM",
                        help="diagram IDs; default: all four")
    selected = parser.parse_args().diagrams or list(factories)
    unknown = set(selected)-factories.keys()
    if unknown:
        parser.error("unknown diagrams: "+", ".join(sorted(unknown)))
    for name in selected:
        diagram = factories[name]()
        (OUT / (diagram["id"] + ".drawio")).write_text(drawio_xml(diagram), encoding="utf-8", newline="\n")
        (OUT / (diagram["id"] + ".svg")).write_text(svg(diagram), encoding="utf-8", newline="\n")
        print("wrote", diagram["id"] + ".drawio", diagram["id"] + ".svg")


if __name__ == "__main__":
    main()
