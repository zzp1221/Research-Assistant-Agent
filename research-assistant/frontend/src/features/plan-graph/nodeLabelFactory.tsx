import type { Task } from '../../contracts/api';
import { getAgentLabel, getStatusTone } from '../shared/agentVisuals';
import { ExperimentSearchNodeLabel } from './ExperimentSearchNodeLabel';

interface CreateTaskNodeLabelOptions {
  assignedTo: string;
  taskName: string;
  status: string;
  step?: number;
  task?: Task;
}

const getPrimaryTaskName = (taskName: string) => taskName.split(/\s+\/\s+/)[0]?.trim() || taskName;

/**
 * A plan node reads like a compact card: step number, status, title and the
 * responsible agent. No decorative plate numbers or proof marks.
 */
export const createTaskNodeLabel = (options: CreateTaskNodeLabelOptions) => {
  const { assignedTo, taskName, status, step, task } = options;
  if (task?.Type === 'experiment_run') {
    return <ExperimentSearchNodeLabel task={task} status={status} step={step} />;
  }
  const tone = getStatusTone(status);
  const isToolCall = task?.Type === 'tool_call';
  const toolID = isToolCall && typeof task?.Inputs?.tool_id === 'string' ? task.Inputs.tool_id : '';
  const toolVersion = isToolCall && typeof task?.Inputs?.tool_version === 'string' ? task.Inputs.tool_version : '';
  const title = isToolCall ? toolID || taskName : getPrimaryTaskName(taskName);

  return (
    <div className="research-assistant-node-label">
      <div className="node-top">
        <span className="node-number">{typeof step === 'number' ? String(step).padStart(2, '0') : '—'}</span>
        <span className="node-status" style={{ color: tone.color }}>
          <i aria-hidden="true" />
          {tone.label}
        </span>
      </div>
      <div className="node-title" title={title}>{title}</div>
      <div className="node-agent">
        {isToolCall ? `工具调用${toolVersion ? ' · v' + toolVersion : ''}` : getAgentLabel(assignedTo)}
      </div>
    </div>
  );
};
