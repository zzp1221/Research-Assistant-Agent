import { Background, Controls, ReactFlow } from '@xyflow/react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import '@xyflow/react/dist/style.css';

export interface GraphCanvasProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  onNodeClick: (_: unknown, node: Node) => void;
}

export default function GraphCanvas(props: GraphCanvasProps) {
  const { nodes, edges, onNodesChange, onEdgesChange, onNodeClick } = props;
  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onNodeClick={onNodeClick}
      onKeyDownCapture={(event) => {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        if (!(event.target instanceof HTMLElement) || !event.target.matches('.react-flow__node')) return;
        const node = nodes.find((candidate) => candidate.id === (event.target as HTMLElement).dataset.id);
        if (!node) return;
        event.preventDefault();
        event.stopPropagation();
        onNodeClick(event, node);
      }}
      nodesConnectable={false}
      nodesDraggable={false}
      onlyRenderVisibleElements={nodes.length > 120}
      fitView
      fitViewOptions={{ padding: 0.2, minZoom: 0.64, maxZoom: 0.96 }}
      minZoom={0.18}
      className="research-assistant-flow"
    >
      <Background color="rgba(31,107,79,0.09)" gap={22} size={1} />
      <Controls showInteractive={false} />
    </ReactFlow>
  );
}
