import type { Page, Server, ServerRuntimeMetrics } from "@/contracts/types";
import { fetchJson } from "@/shared/api/client";

export const MONITORING_REFRESH_MS = 15_000;

export type MonitoringAgent = {
  id: string;
  serverId: string;
  version?: string;
  protocolVersion?: number;
  status: string;
  lastHeartbeatAt?: string;
  lastSampleAt?: string;
  lastError?: string;
};

export type MonitoringProject = {
  projectId: string;
  projectName: string;
  applicationId: string;
  applicationName: string;
  status: string;
  runningContainers: number;
  alerts: Array<{ level: string; code?: string; summary?: string }>;
};

export type MonitoringServer = {
  serverId: string;
  serverName: string;
  publicHost?: string;
  swarm?: boolean;
  swarmVerificationStatus?: ServerRuntimeMetrics["swarmVerificationStatus"];
  swarmVerificationError?: string;
  swarmNodeId?: string;
  swarmRole?: string;
  swarmConnectionMode?: "public" | "private" | "unknown";
  swarmAdvertiseAddr?: string;
  swarmManagerAddress?: string;
  connectionStatus?: "online" | "offline" | "unknown";
  sampleStatus?: "fresh" | "stale" | "unknown";
  agentStatus: string;
  agentVersion?: string;
  lastHeartbeatAt?: string | null;
  lastSampleAt?: string | null;
  alerts: Array<{ level: string; code?: string; summary?: string }>;
  cpuPercent?: number;
  memoryPercent?: number;
  diskPercent?: number;
  dockerDiskBytes?: number;
};

export type SwarmOverview = {
  clusterCount?: number;
  unverifiedMemberCount?: number;
  status?: "ready" | "not_enabled" | "unverified" | "stale" | "error";
  clusters?: Array<Record<string, unknown>>;
  agents?: unknown[];
  alerts?: Array<Record<string, unknown>>;
  lastSampleAt?: string | null;
  networks?: Array<Record<string, unknown>>;
  nodes?: Array<Record<string, unknown>>;
  services?: Array<Record<string, unknown>>;
  snapshots?: Array<Record<string, unknown>>;
  tasks?: Array<Record<string, unknown>>;
};

export function fetchMonitoringAgents() {
  return fetchJson<Page<MonitoringAgent>>("monitoring/agents");
}

export function fetchMonitoringServers() {
  return fetchJson<{ items: MonitoringServer[]; generatedAt: string }>("monitoring/servers");
}

export function fetchMonitoringProjects() {
  return fetchJson<{ items: MonitoringProject[]; generatedAt: string }>("monitoring/projects");
}

export function fetchSwarmOverview() {
  return fetchJson<SwarmOverview>("monitoring/swarm");
}

export function fetchMonitoringServerOptions() {
  return fetchJson<Page<Server>>("servers");
}
