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
      nodesConnectable={false}
      nodesDraggable={false}
      onlyRenderVisibleElements={nodes.length > 120}
      fitView
      fitViewOptions={{ padding: 0.18, minZoom: 0.68, maxZoom: 1 }}
      minZoom={0.18}
      className="instrument-grid"
    >
      <Background color="rgba(120,135,145,0.18)" gap={24} size={1} />
      <Controls showInteractive={false} />
    </ReactFlow>
  );
}