export type Side = 'left' | 'right' | 'top' | 'bottom';

export interface PortSpec {
  inputs: number;
  outputs: number;
}

export type NodePorts = Record<Side, PortSpec>;

export interface ConnectionType {
  id: string;
  label: string;
  color: string;
  dashed?: boolean;
  strict?: boolean;
}

export interface ConnectionTemplate {
  id: string;
  name: string;
  sourceKind: string;
  targetKind: string;
  type: string;
  label: string;
  sourceSide?: Side;
  targetSide?: Side;
}

export interface McpToolContract {
  name: string;
  description?: string;
  inputSchema: Record<string, unknown>;
}

export interface McpServerContract {
  name: string;
  transport: 'stdio' | 'http' | 'sse';
  command?: string;
  args?: string[];
  url?: string;
  envKeys?: string[];
  protocolVersion?: string;
  capabilities?: Record<string, unknown>;
  tools?: McpToolContract[];
  resources?: string[];
  prompts?: string[];
}

export interface GraphSchema {
  $schema: string;
  version: number;
  nodePorts: Record<string, NodePorts>;
  connectionTypes: ConnectionType[];
  templates: ConnectionTemplate[];
  contracts?: {
    mcpServer?: McpServerContract;
  };
}

const ports = (left: PortSpec, right: PortSpec, top: PortSpec, bottom: PortSpec): NodePorts => ({ left, right, top, bottom });
const port = (inputs: number, outputs: number): PortSpec => ({ inputs, outputs });

export const DEFAULT_GRAPH_SCHEMA: GraphSchema = {
  $schema: 'https://dockweave.local/schema/agent-graph/v1.json',
  version: 1,
  nodePorts: {
    org: ports(port(0, 0), port(0, 0), port(0, 0), port(0, 2)),
    unit: ports(port(0, 0), port(0, 0), port(1, 0), port(0, 4)),
    agent: ports(port(1, 1), port(1, 1), port(1, 0), port(0, 1)),
    workspace: ports(port(0, 1), port(1, 0), port(0, 0), port(0, 0)),
    memory: ports(port(1, 0), port(0, 0), port(0, 0), port(0, 0)),
    credential: ports(port(1, 0), port(0, 0), port(0, 0), port(0, 0)),
    knowledge: ports(port(0, 0), port(0, 0), port(4, 0), port(0, 0)),
    mcp: ports(port(1, 1), port(1, 1), port(0, 0), port(0, 0)),
    gate: ports(port(1, 1), port(1, 1), port(0, 0), port(0, 0)),
    http: ports(port(1, 1), port(1, 1), port(0, 0), port(0, 0)),
    skill: ports(port(1, 0), port(0, 1), port(0, 0), port(0, 0)),
  },
  connectionTypes: [
    { id: 'rw', label: 'read-write', color: '#3fb950', strict: true },
    { id: 'ro', label: 'read-only', color: '#d29922', strict: true },
    { id: 'memory', label: 'memory', color: '#bc8cff' },
    { id: 'credential', label: 'credential', color: '#d29922' },
    { id: 'knowledge', label: 'knowledge', color: '#39c5cf' },
    { id: 'unit', label: 'unit membership', color: '#3fb950' },
    { id: 'org', label: 'organization', color: '#58a6ff' },
    { id: 'custom', label: 'custom', color: '#8b949e', dashed: true },
    { id: 'mcp', label: 'MCP server', color: '#bc8cff' },
    { id: 'gate', label: 'deterministic gate', color: '#d29922' },
    { id: 'http', label: 'HTTP API', color: '#39c5cf' },
    { id: 'chain', label: 'agent chain', color: '#58a6ff', dashed: true },
  ],
  templates: [
    { id: 'agent-workspace', name: 'Agent → Workspace', sourceKind: 'agent', targetKind: 'workspace', type: 'rw', label: 'rw', sourceSide: 'left', targetSide: 'right' },
    { id: 'agent-memory', name: 'Agent → Memory', sourceKind: 'agent', targetKind: 'memory', type: 'memory', label: 'memory', sourceSide: 'right', targetSide: 'left' },
    { id: 'agent-credential', name: 'Agent → Credential', sourceKind: 'agent', targetKind: 'credential', type: 'credential', label: 'credential', sourceSide: 'right', targetSide: 'left' },
    { id: 'agent-knowledge', name: 'Agent → Knowledge', sourceKind: 'agent', targetKind: 'knowledge', type: 'knowledge', label: 'knowledge', sourceSide: 'bottom', targetSide: 'top' },
    { id: 'unit-agent', name: 'Unit → Agent', sourceKind: 'unit', targetKind: 'agent', type: 'unit', label: 'member', sourceSide: 'bottom', targetSide: 'top' },
    { id: 'agent-mcp', name: 'Agent → MCP server', sourceKind: 'agent', targetKind: 'mcp', type: 'mcp', label: 'mcp', sourceSide: 'right', targetSide: 'left' },
    { id: 'agent-gate', name: 'Agent → Gate', sourceKind: 'agent', targetKind: 'gate', type: 'gate', label: 'gate', sourceSide: 'right', targetSide: 'left' },
    { id: 'agent-http', name: 'Agent → HTTP API', sourceKind: 'agent', targetKind: 'http', type: 'http', label: 'http', sourceSide: 'right', targetSide: 'left' },
    { id: 'agent-chain', name: 'Agent → Agent (chain)', sourceKind: 'agent', targetKind: 'agent', type: 'chain', label: 'chain', sourceSide: 'right', targetSide: 'left' },
  ],
  contracts: {
    mcpServer: {
      name: 'filesystem-mcp',
      transport: 'stdio',
      command: 'npx',
      args: ['-y', '@modelcontextprotocol/server-filesystem', '/workspace'],
      envKeys: [],
      protocolVersion: '2025-06-18',
      capabilities: { tools: {} },
      tools: [
        {
          name: 'read_file',
          description: 'Read a file from the workspace',
          inputSchema: {
            type: 'object',
            properties: { path: { type: 'string' } },
            required: ['path'],
          },
        },
      ],
      resources: [],
      prompts: [],
    },
  },
};
