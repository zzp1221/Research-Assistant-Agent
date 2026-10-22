import assert from 'node:assert/strict';
import test from 'node:test';
import { parseBenchmarkReport } from '../src/features/benchmark/benchmarkReport.ts';

const validation = {
  version: 'benchmark.validation/v1',
  status: 'validated',
  contract_id: 'contract-1',
  primary_metric: 'macro_f1',
  direction: 'maximize',
  public_metrics: { accuracy: 0.75, macro_f1: 0.74 },
  hidden_metrics: { accuracy: 1, macro_f1: 1 },
  hidden_sample_count: 4,
  target_reached: true,
  protected_files_valid: true,
  checks: ['public split and evaluator hashes matched', 'metrics were recomputed by the backend'],
  validated_at: '2026-10-22T00:00:00Z',
};

test('parses a benchmark validation report and marks the primary metric', () => {
  const parsed = parseBenchmarkReport(JSON.stringify(validation));
  assert.ok(parsed);
  assert.equal(parsed.kind, 'validation');
  assert.equal(parsed.validation.primaryMetric, 'macro_f1');
  assert.equal(parsed.validation.direction, 'maximize');
  assert.equal(parsed.validation.hiddenMetrics[0].name, 'macro_f1');
  assert.equal(parsed.validation.hiddenMetrics[0].isPrimary, true);
  assert.equal(parsed.validation.hiddenSampleCount, 4);
  assert.equal(parsed.validation.targetReached, true);
  assert.equal(parsed.validation.checks.length, 2);
});

test('parses a run metrics report from benchmark_execute', () => {
  const parsed = parseBenchmarkReport(JSON.stringify({
    status: 'ok',
    mode: 'run',
    sample_count: 24,
    metrics: { accuracy: 0.8, macro_f1: 0.79 },
  }));
  assert.ok(parsed);
  assert.equal(parsed.kind, 'run');
  assert.equal(parsed.run.sampleCount, 24);
  assert.deepEqual(parsed.run.metrics.map((row) => row.name).sort(), ['accuracy', 'macro_f1']);
});

test('parses a dataset audit report', () => {
  const parsed = parseBenchmarkReport(JSON.stringify({
    version: 'benchmark.audit/v1',
    task_type: 'classification',
    input_columns: ['review'],
    target_column: 'label',
    confidence: 1,
    class_counts: { positive: 12, negative: 12 },
    blocking_issues: [],
  }));
  assert.ok(parsed);
  assert.equal(parsed.kind, 'audit');
  assert.equal(parsed.audit.taskType, 'classification');
  assert.equal(parsed.audit.classCounts.length, 2);
});

test('returns null for unrelated JSON', () => {
  assert.equal(parseBenchmarkReport(JSON.stringify({ hello: 'world' })), null);
  assert.equal(parseBenchmarkReport('not json'), null);
  assert.equal(parseBenchmarkReport(''), null);
});

test('accepts a double-encoded JSON string', () => {
  const parsed = parseBenchmarkReport(JSON.stringify(JSON.stringify(validation)));
  assert.ok(parsed);
  assert.equal(parsed.kind, 'validation');
});
