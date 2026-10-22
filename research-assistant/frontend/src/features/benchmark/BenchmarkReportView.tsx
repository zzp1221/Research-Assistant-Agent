import { useMemo } from 'react';
import { AlertTriangle, CheckCircle2, Gauge, ShieldCheck, ShieldX, Target, XCircle } from 'lucide-react';
import {
  benchmarkStatusLabel,
  formatBenchmarkScore,
  parseBenchmarkReport,
} from './benchmarkReport';

export interface BenchmarkReportViewProps {
  raw: string;
  expanded?: boolean;
}

function MetricTable({
  title,
  rows,
  direction,
}: {
  title: string;
  rows: { name: string; value: number; isPrimary: boolean }[];
  direction: string;
}) {
  return (
    <section className="border border-slate-200 bg-white">
      <header className="flex items-center justify-between border-b border-slate-200 bg-slate-50 px-3 py-2">
        <span className="text-xs font-semibold text-slate-700">{title}</span>
        <span className="text-[10px] text-slate-500">{direction === 'minimize' ? '越低越好' : direction === 'maximize' ? '越高越好' : '方向未声明'}</span>
      </header>
      {rows.length === 0 ? (
        <p className="px-3 py-4 text-xs text-slate-400">没有可显示的指标。</p>
      ) : (
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-slate-100 text-[10px] uppercase tracking-wide text-slate-400">
              <th className="px-3 py-1.5 text-left font-medium">指标</th>
              <th className="px-3 py-1.5 text-right font-medium">值</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.name} className={row.isPrimary ? 'bg-emerald-50/60' : ''}>
                <td className="px-3 py-1.5 font-mono text-[11px] text-slate-700">
                  {row.name}
                  {row.isPrimary && <span className="ml-1 rounded bg-emerald-100 px-1 py-0.5 text-[9px] font-semibold text-emerald-700">primary</span>}
                </td>
                <td className="px-3 py-1.5 text-right font-mono font-semibold text-slate-900">{formatBenchmarkScore(row.value)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

export function BenchmarkReportView({ raw, expanded = false }: BenchmarkReportViewProps) {
  const parsed = useMemo(() => parseBenchmarkReport(raw), [raw]);
  if (!parsed) {
    return (
      <div className="flex min-h-40 flex-col items-center justify-center gap-2 border border-amber-200 bg-amber-50 px-6 text-center text-sm text-amber-800" role="alert">
        <AlertTriangle className="h-5 w-5" />
        <div className="font-semibold">无法解析 Benchmark 结果</div>
        <div className="text-xs">该节点输出不是已知的 benchmark 报告结构，可在“原始 JSON”中检查。</div>
      </div>
    );
  }

  if (parsed.kind === 'validation' && parsed.validation) {
    const report = parsed.validation;
    const passed = report.status === 'validated' && report.targetReached;
    const Icon = passed ? CheckCircle2 : XCircle;
    return (
      <div className={`flex flex-col gap-3 bg-white p-4 ${expanded ? 'min-h-full' : ''}`}>
        <div className={`flex flex-wrap items-center justify-between gap-2 border px-3 py-2 ${passed ? 'border-emerald-200 bg-emerald-50' : 'border-amber-200 bg-amber-50'}`}>
          <div className={`flex items-center gap-2 text-sm font-semibold ${passed ? 'text-emerald-800' : 'text-amber-800'}`}>
            <Icon className="h-4 w-4" />
            {benchmarkStatusLabel(report.status)}
            <span className="text-[11px] font-normal text-slate-500">{report.version}</span>
          </div>
          <div className="flex items-center gap-2 text-[11px] text-slate-600">
            <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 ${report.targetReached ? 'bg-emerald-100 text-emerald-700' : 'bg-rose-100 text-rose-700'}`}>
              <Target className="h-3 w-3" />
              目标{report.targetReached ? '已达到' : '未达到'}
            </span>
            <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 ${report.protectedFilesValid ? 'bg-emerald-100 text-emerald-700' : 'bg-rose-100 text-rose-700'}`}>
              {report.protectedFilesValid ? <ShieldCheck className="h-3 w-3" /> : <ShieldX className="h-3 w-3" />}
              受保护文件{report.protectedFilesValid ? '完好' : '异常'}
            </span>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
          <div className="border border-slate-200 bg-slate-50 px-3 py-2">
            <div className="text-[10px] font-semibold uppercase tracking-wide text-slate-400">主指标</div>
            <div className="mt-1 font-mono text-sm font-semibold text-slate-900">{report.primaryMetric || '-'}</div>
          </div>
          <div className="border border-slate-200 bg-slate-50 px-3 py-2">
            <div className="text-[10px] font-semibold uppercase tracking-wide text-slate-400">隐藏样本</div>
            <div className="mt-1 font-mono text-sm font-semibold text-slate-900">{report.hiddenSampleCount}</div>
          </div>
          <div className="border border-slate-200 bg-slate-50 px-3 py-2">
            <div className="text-[10px] font-semibold uppercase tracking-wide text-slate-400">Contract</div>
            <div className="mt-1 truncate font-mono text-[11px] text-slate-700" title={report.contractId}>{report.contractId || '-'}</div>
          </div>
        </div>

        <div className="grid gap-3 md:grid-cols-2">
          <MetricTable title="公开集指标（adapter 自报）" rows={report.publicMetrics} direction={report.direction} />
          <MetricTable title="隐藏标签重算指标（后端）" rows={report.hiddenMetrics} direction={report.direction} />
        </div>

        <section className="border border-slate-200 bg-white">
          <header className="border-b border-slate-200 bg-slate-50 px-3 py-2 text-xs font-semibold text-slate-700">验收检查</header>
          <ul className="divide-y divide-slate-100">
            {report.checks.map((check) => (
              <li key={check} className="flex items-center gap-2 px-3 py-1.5 text-[11px] text-slate-600">
                <CheckCircle2 className="h-3 w-3 text-emerald-500" />
                {check}
              </li>
            ))}
            {report.checks.length === 0 && <li className="px-3 py-2 text-[11px] text-slate-400">没有检查记录。</li>}
          </ul>
        </section>

        <details className="border border-slate-200 bg-slate-50">
          <summary className="cursor-pointer px-3 py-2 text-[11px] font-semibold text-slate-600">原始 JSON</summary>
          <pre className="max-h-64 overflow-auto border-t border-slate-200 bg-white p-3 text-[10px] leading-5 text-slate-600">{raw}</pre>
        </details>
      </div>
    );
  }

  if (parsed.kind === 'run' && parsed.run) {
    const report = parsed.run;
    return (
      <div className="flex flex-col gap-3 bg-white p-4">
        <div className="flex items-center gap-2 border border-slate-200 bg-slate-50 px-3 py-2 text-sm font-semibold text-slate-800">
          <Gauge className="h-4 w-4 text-blue-600" />
          Benchmark 执行结果
          <span className="text-[11px] font-normal text-slate-500">{report.status}</span>
        </div>
        <MetricTable title="运行指标" rows={report.metrics.map((metric) => ({ ...metric, isPrimary: false }))} direction={report.direction} />
        {report.sampleCount !== null && <p className="text-[11px] text-slate-500">样本数：{report.sampleCount}</p>}
        {report.reason && <p className="text-[11px] text-rose-600">{report.reason}</p>}
        <details className="border border-slate-200 bg-slate-50">
          <summary className="cursor-pointer px-3 py-2 text-[11px] font-semibold text-slate-600">原始 JSON</summary>
          <pre className="max-h-64 overflow-auto border-t border-slate-200 bg-white p-3 text-[10px] leading-5 text-slate-600">{raw}</pre>
        </details>
      </div>
    );
  }

  if (parsed.kind === 'audit' && parsed.audit) {
    const audit = parsed.audit;
    return (
      <div className="flex flex-col gap-3 bg-white p-4">
        <div className="flex items-center gap-2 border border-slate-200 bg-slate-50 px-3 py-2 text-sm font-semibold text-slate-800">
          <Gauge className="h-4 w-4 text-blue-600" />
          Benchmark 数据审计
        </div>
        <div className="grid gap-3 md:grid-cols-3">
          <div className="border border-slate-200 px-3 py-2">
            <div className="text-[10px] uppercase text-slate-400">任务类型</div>
            <div className="font-mono text-sm text-slate-800">{audit.taskType}</div>
          </div>
          <div className="border border-slate-200 px-3 py-2">
            <div className="text-[10px] uppercase text-slate-400">输入列</div>
            <div className="font-mono text-sm text-slate-800">{audit.inputColumns.join(', ') || '-'}</div>
          </div>
          <div className="border border-slate-200 px-3 py-2">
            <div className="text-[10px] uppercase text-slate-400">目标列</div>
            <div className="font-mono text-sm text-slate-800">{audit.targetColumn || '-'}</div>
          </div>
        </div>
        <div className="text-[11px] text-slate-500">映射置信度：{(audit.confidence * 100).toFixed(0)}%</div>
        {audit.classCounts.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {audit.classCounts.map((item) => (
              <span key={item.label} className="rounded bg-slate-100 px-2 py-0.5 text-[10px] text-slate-600">
                {item.label}: {item.count}
              </span>
            ))}
          </div>
        )}
        {audit.blockingIssues.length > 0 && (
          <ul className="space-y-1">
            {audit.blockingIssues.map((issue) => (
              <li key={issue} className="flex items-center gap-1 text-[11px] text-rose-700">
                <AlertTriangle className="h-3 w-3" />{issue}
              </li>
            ))}
          </ul>
        )}
        <details className="border border-slate-200 bg-slate-50">
          <summary className="cursor-pointer px-3 py-2 text-[11px] font-semibold text-slate-600">原始 JSON</summary>
          <pre className="max-h-64 overflow-auto border-t border-slate-200 bg-white p-3 text-[10px] leading-5 text-slate-600">{raw}</pre>
        </details>
      </div>
    );
  }

  return null;
}
