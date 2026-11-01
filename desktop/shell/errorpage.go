package shell

import (
	"html"
	"strings"
)

func errorPageHTML(status Status) string {
	errorText := status.Error
	if strings.TrimSpace(errorText) == "" {
		errorText = "服务未能启动。"
	}
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>ResearchAssistant 启动失败</title>
<style>
  :root { color-scheme: light dark; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; font-family: "Segoe UI", "Microsoft YaHei", sans-serif; background: #f6f7f9; color: #1f2328; }
  main { width: min(720px, 92vw); padding: 32px; border-radius: 16px; background: #fff; box-shadow: 0 18px 48px rgba(15, 23, 42, .12); }
  h1 { margin: 0 0 8px; font-size: 22px; }
  p { margin: 8px 0; line-height: 1.6; color: #4b5563; }
  pre { padding: 12px 14px; border-radius: 10px; background: #f3f4f6; color: #b42318; white-space: pre-wrap; word-break: break-word; }
  code { padding: 2px 6px; border-radius: 6px; background: #eef2f7; }
  .actions { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 20px; }
  button { padding: 9px 16px; border: 0; border-radius: 9px; background: #2563eb; color: #fff; font-size: 14px; cursor: pointer; }
  button.secondary { background: #e5e7eb; color: #111827; }
  .hint { font-size: 12px; color: #6b7280; margin-top: 14px; }
</style>
</head>
<body>
<main>
  <h1>ResearchAssistant 启动失败</h1>
  <p>桌面外壳无法启动本地服务。请把下面的日志目录和诊断包提供给支持人员。</p>
  <pre>` + html.EscapeString(errorText) + `</pre>
  <p>日志目录：<code>` + html.EscapeString(status.LogDir) + `</code></p>
  <div class="actions">
    <button onclick="desktopCall('openLogs')">打开日志目录</button>
    <button class="secondary" onclick="desktopCall('exportDiagnostics')">导出诊断包</button>
    <button class="secondary" onclick="desktopCall('retry')">重试</button>
  </div>
  <p class="hint">诊断包会脱敏 API Key 与 token，只包含日志尾部、设置、能力矩阵和最近失败计划。</p>
</main>
<script>
function desktopCall(name) {
  const bridge = window.__RA_DESKTOP__;
  if (bridge && typeof bridge[name] === "function") { bridge[name](); return; }
  if (name === "retry") { fetch("/desktop/retry", { method: "POST" }).then(() => location.reload()); }
  if (name === "openLogs") { fetch("/desktop/open-logs", { method: "POST" }).catch(() => {}); }
  if (name === "exportDiagnostics") { fetch("/desktop/diagnostics", { method: "POST" }).then(r => r.json()).then(v => alert("诊断包: " + v.path)).catch(e => alert(String(e))); }
}
</script>
</body>
</html>`
}

func loadingPageHTML(status Status) string {
	_ = status
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>ResearchAssistant 正在启动</title>
<style>
  :root { color-scheme: light dark; }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; font-family: "Segoe UI", "Microsoft YaHei", sans-serif; background: #0f172a; color: #e2e8f0; }
  .card { text-align: center; }
  .spinner { width: 38px; height: 38px; margin: 0 auto 18px; border: 3px solid rgba(226,232,240,.2); border-top-color: #60a5fa; border-radius: 50%; animation: spin 1s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  p { color: #94a3b8; font-size: 14px; }
</style>
</head>
<body>
<div class="card">
  <div class="spinner"></div>
  <h1>正在启动本地服务…</h1>
  <p>首次启动可能需要准备 Python 运行时。</p>
</div>
</body>
</html>`
}
