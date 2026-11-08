import type { ContextReport } from '../../contracts/api';

export function ContextStatus({ report }: { report?: ContextReport }) {
  if (!report?.model) return null;
  const observation = report.input_observation;
  const compaction = report.last_compaction;
  return (
    <details className="mt-2 text-[11px] leading-5 text-muted">
      <summary className="cursor-pointer">{report.model} · 上下文容量 {report.capacity.toLocaleString()} tokens</summary>
      <p>本次输入：{observation.tokens === undefined ? '未知' : `${observation.tokens.toLocaleString()} tokens`}（{observation.confidence === 'exact' ? '服务商计数' : observation.confidence === 'estimated' ? '估算' : '计数未知'}），可用输入预算 {report.input_budget.toLocaleString()} tokens</p>
      <p>压缩模式：{report.compaction_mode} · 模型 revision {report.profile_revision} · 窗口版本 {report.generation}</p>
      {report.http_requests && <p>本次请求：生成 {report.http_requests.generation} · 压缩 {report.http_requests.compaction} · 计数 {report.http_requests.counting}</p>}
      {compaction && <p>最近压缩：{compaction.status} · {new Date(compaction.at).toLocaleString()} · {compaction.request_attempts} 次额外请求 · {compaction.mode === 'server' ? '生成请求总耗时 ' : ''}{compaction.latency_ms} ms · 成本{compaction.cost_usd === undefined ? '未知' : ` $${compaction.cost_usd}`}</p>}
    </details>
  );
}
