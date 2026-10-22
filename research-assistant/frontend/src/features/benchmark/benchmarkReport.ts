export interface BenchmarkMetricRow {
  name: string;
  value: number;
  isPrimary: boolean;
}

export interface BenchmarkValidationView {
  version: string;
  status: string;
  contractId: string;
  primaryMetric: string;
  direction: 'maximize' | 'minimize' | 'unknown';
  publicMetrics: BenchmarkMetricRow[];
  hiddenMetrics: BenchmarkMetricRow[];
  hiddenSampleCount: number;
  targetReached: boolean;
  protectedFilesValid: boolean;
  checks: string[];
  validatedAt: string;
}

export interface BenchmarkRunMetricsView {
  direction: string;
  status: string;
  metrics: { name: string; value: number }[];
  sampleCount: number | null;
  reason: string;
}

export interface BenchmarkAuditView {
  taskType: string;
  inputColumns: string[];
  targetColumn: string;
  confidence: number;
  rowCount: number | null;
  blockingIssues: string[];
  classCounts: { label: string; count: number }[];
}

export interface BenchmarkReportView {
  kind: 'validation' | 'run' | 'audit';
  validation?: BenchmarkValidationView;
  run?: BenchmarkRunMetricsView;
  audit?: BenchmarkAuditView;
}

type JsonRecord = Record<string, unknown>;

const asObject = (value: unknown): JsonRecord | null => {
  if (value && typeof value === 'object' && !Array.isArray(value)) return value as JsonRecord;
  return null;
};

const decode = (raw: string | unknown): unknown => {
  let value: unknown = raw;
  for (let pass = 0; pass < 2 && typeof value === 'string'; pass += 1) {
    const text = value.trim();
    if (!text) return null;
    try {
      value = JSON.parse(text);
    } catch {
      return null;
    }
  }
  return value;
};

const metricRows = (raw: unknown, primary: string): BenchmarkMetricRow[] => {
  const source = asObject(raw);
  if (!source) return [];
  return Object.entries(source)
    .map(([name, value]) => ({ name, value: Number(value) }))
    .filter((row) => Number.isFinite(row.value))
    .map((row) => ({ ...row, isPrimary: row.name === primary }))
    .sort((left, right) => Number(right.isPrimary) - Number(left.isPrimary) || left.name.localeCompare(right.name));
};

export function parseBenchmarkReport(raw: string | unknown): BenchmarkReportView | null {
  const decoded = decode(raw);
  const object = asObject(decoded);
  if (!object) return null;

  if (object.version === 'benchmark.validation/v1') {
    const primary = String(object.primary_metric || '');
    return {
      kind: 'validation',
      validation: {
        version: String(object.version),
        status: String(object.status || 'unknown'),
        contractId: String(object.contract_id || ''),
        primaryMetric: primary,
        direction: object.direction === 'minimize' ? 'minimize' : object.direction === 'maximize' ? 'maximize' : 'unknown',
        publicMetrics: metricRows(object.public_metrics, primary),
        hiddenMetrics: metricRows(object.hidden_metrics, primary),
        hiddenSampleCount: Number(object.hidden_sample_count || 0),
        targetReached: Boolean(object.target_reached),
        protectedFilesValid: Boolean(object.protected_files_valid),
        checks: Array.isArray(object.checks) ? object.checks.map(String) : [],
        validatedAt: String(object.validated_at || ''),
      },
    };
  }

  // research_coding_agent's benchmark validation report is a looser shape.
  if (object.status === 'validated' && object.metrics && !object.primary_metric) {
    return {
      kind: 'run',
      run: {
        direction: 'unknown',
        status: String(object.status),
        metrics: metricRows(object.metrics, ''),
        sampleCount: Number.isFinite(Number(object.sample_count)) ? Number(object.sample_count) : null,
        reason: String(object.reason || ''),
      },
    };
  }

  if (object.metrics && object.mode) {
    return {
      kind: 'run',
      run: {
        direction: 'unknown',
        status: String(object.status || 'unknown'),
        metrics: metricRows(object.metrics, ''),
        sampleCount: Number.isFinite(Number(object.sample_count)) ? Number(object.sample_count) : null,
        reason: String(object.reason || ''),
      },
    };
  }

  if (object.task_type && Array.isArray(object.input_columns)) {
    const classCountsRaw = asObject(object.class_counts);
    const metadata = asObject(object.metadata);
    return {
      kind: 'audit',
      audit: {
        taskType: String(object.task_type),
        inputColumns: object.input_columns.map(String),
        targetColumn: String(object.target_column || ''),
        confidence: Number(object.confidence || 0),
        rowCount: metadata && Number.isFinite(Number(metadata.row_count)) ? Number(metadata.row_count) : null,
        blockingIssues: Array.isArray(object.blocking_issues) ? object.blocking_issues.map(String) : [],
        classCounts: classCountsRaw
          ? Object.entries(classCountsRaw).map(([label, count]) => ({ label, count: Number(count) }))
          : [],
      },
    };
  }

  return null;
}

export const formatBenchmarkScore = (value: number): string => {
  if (!Number.isFinite(value)) return '-';
  if (Math.abs(value) >= 1000) return value.toFixed(1);
  if (Number.isInteger(value)) return String(value);
  return value.toFixed(4).replace(/0+$/, '').replace(/\.$/, '');
};

export const benchmarkStatusLabel = (status: string): string =>
  ({
    validated: '已通过隐藏标签验收',
    not_validated: '未达到冻结目标',
    ok: '运行成功',
    failed: '运行失败',
    unknown: '状态未知',
  }[status] ?? status);
