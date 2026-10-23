import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { BenchmarkReportView } from '../../src/features/benchmark/BenchmarkReportView';

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

describe('BenchmarkReportView', () => {
  it('renders a readable validation report with public and hidden metrics', () => {
    render(<BenchmarkReportView raw={JSON.stringify(validation)} />);

    expect(screen.getByText('已通过隐藏标签验收')).toBeInTheDocument();
    expect(screen.getByText('目标已达到')).toBeInTheDocument();
    expect(screen.getByText('受保护文件完好')).toBeInTheDocument();
    expect(screen.getByText('公开集指标（adapter 自报）')).toBeInTheDocument();
    expect(screen.getByText('隐藏标签重算指标（后端）')).toBeInTheDocument();
    expect(screen.getAllByText('macro_f1').length).toBeGreaterThan(0);
    expect(screen.getByText('public split and evaluator hashes matched')).toBeInTheDocument();
    expect(screen.getByText('原始 JSON')).toBeInTheDocument();
  });

  it('renders run metrics from benchmark_execute', () => {
    render(<BenchmarkReportView raw={JSON.stringify({ status: 'ok', mode: 'run', sample_count: 24, metrics: { accuracy: 0.8, macro_f1: 0.79 } })} />);

    expect(screen.getByText('Benchmark 执行结果')).toBeInTheDocument();
    expect(screen.getByText('样本数：24')).toBeInTheDocument();
    expect(screen.getByText('accuracy')).toBeInTheDocument();
  });

  it('shows a clear fallback for unrelated JSON', () => {
    render(<BenchmarkReportView raw={JSON.stringify({ hello: 'world' })} />);
    expect(screen.getByText('无法解析 Benchmark 结果')).toBeInTheDocument();
  });
});
