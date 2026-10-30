import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { createTaskNodeLabel } from '../../src/features/plan-graph/nodeLabelFactory';
import type { Task } from '../../src/contracts/api';

const toolTask = {
  ID: 'tool-1',
  Name: 'Echo',
  Type: 'tool_call',
  Description: '',
  AssignedTo: 'tool_executor',
  Status: 'pending',
  Dependencies: [],
  Inputs: { tool_id: 'native:echo', tool_version: '1.0.0', arguments: { message: 'hi' } },
  RequiredArtifacts: [],
  OutputArtifacts: ['tool_result'],
} as unknown as Task;

describe('tool_call node label', () => {
  it('shows the tool id and version instead of a generic agent name', () => {
    render(createTaskNodeLabel({ assignedTo: 'tool_executor', taskName: 'Echo', status: 'pending', step: 1, task: toolTask }));
    expect(screen.getByText('native:echo')).toBeInTheDocument();
    expect(screen.getByText('工具调用 · v1.0.0')).toBeInTheDocument();
  });
});
