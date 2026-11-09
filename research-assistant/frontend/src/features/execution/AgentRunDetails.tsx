import type { Task } from '../../contracts/api';

const phaseLabels: Record<string, string> = {
  running: '执行中', completed: '已完成', stopped: '已停止', awaiting_approval: '等待工具审批',
  retryable: '等待恢复', outcome_unknown: '等待确认工具结果',
};
const failureLabels: Record<string, string> = {
  transient: '暂时性故障', environment: '运行环境问题', output: '输出验收失败',
  missing_input: '缺少输入', permission: '权限或审批限制', budget: '预算已用尽', canceled: '已取消', deterministic: '确定性执行失败',
};
const stageLabels: Record<string, string> = { model: '模型调用', tool: '工具执行', output: '输出验收', scheduler: '任务调度' };

export function AgentRunDetails({ task }: { task: Task }) {
  const run = task.AgentRun;
  const recovery = task.Recovery;
  const failure = task.Failure;
  const proposal = task.ReplanProposal;
  if (!run && !recovery && !failure && !proposal) return null;
  const lastDecision = recovery?.decisions?.at(-1);
  return (
    <section aria-label="Agent 执行与恢复" className="border-b border-line bg-surface px-4 py-3 text-[11px] text-ink-soft">
      <h4 className="text-[11px] font-semibold text-ink">执行与恢复</h4>
      {run && <>
        <p className="mt-1">当前阶段：{phaseLabels[run.status] ?? run.status}{run.status === 'running' && run.pending_call_id ? ' · 执行工具' : ''}</p>
        <p className="mt-1 font-mono">模型步数 {run.steps}/{run.max_steps} · 工具调用 {run.tool_calls}/{run.max_tool_calls}</p>
        {run.stop_reason && <p className="mt-1 break-words text-proof">停止原因：{run.stop_reason}</p>}
      </>}
      {recovery && <>
        <p className="mt-1">恢复动作剩余 {Math.max(0, recovery.max_actions - recovery.used_actions)}/{recovery.max_actions} · HTTP 尝试 {recovery.http_attempts}/{recovery.max_http_attempts}</p>
        {lastDecision && <p className="mt-1 break-words">最近恢复：{lastDecision.action} · {lastDecision.reason}</p>}
      </>}
      {failure && <div className="mt-2 border-l-2 border-proof/50 pl-2">
        <p>失败类型：{failureLabels[failure.class] ?? failure.class} · {stageLabels[failure.stage] ?? failure.stage}</p>
        <p className="mt-1 break-words">{failure.message}</p>
        <p className="mt-1 font-mono text-[10px] text-muted">{failure.code} · {failure.recoverable ? '可在剩余额度内恢复' : '需要处理原因后继续'}</p>
      </div>}
      {proposal && <div className="mt-2 border-l-2 border-warn/50 pl-2">
        <p>重规划提案{proposal.requires_review ? ' · 待审阅' : ''} · r{proposal.base_revision}</p>
        <p className="mt-1 break-words">{proposal.reason}</p>
        {Boolean(proposal.invalidate_artifacts?.length) && <p className="mt-1">需重新生成的产物：{proposal.invalidate_artifacts?.join(', ')}</p>}
      </div>}
    </section>
  );
}
