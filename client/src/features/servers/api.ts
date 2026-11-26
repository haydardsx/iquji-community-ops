import type { OperationRun, Page, Server, ServerRuntimeMetrics } from "@/contracts/types";
import { fetchJson } from "@/shared/api/client";
import type { OperationLogEntry } from "@/shared/operations/log-api";

export type ServerInput = {
  name: string;
  publicHost: string;
  privateHost: string;
  port: number;
  sshUser: string;
  environment?: string;
  labels?: string[];
  platformProfile?: Record<string, unknown>;
};

export function fetchServers() {
  return fetchJson<Page<Server>>("servers");
}

export function createServer(input: ServerInput) {
  return fetchJson<Server>("servers", { method: "POST", body: input });
}

export function updateServer(serverId: string, input: ServerInput) {
  return fetchJson<Server>(`servers/${serverId}`, { method: "PUT", body: input });
}

export function deleteServer(serverId: string) {
  return fetchJson<{ ok: boolean }>(`servers/${serverId}`, { method: "DELETE" });
}

export type RuntimeCheck = {
  id?: string;
  code?: string;
  name?: string;
  outcome?: string;
  status?: string;
  summary?: string;
};
export type RuntimeProcess = {
  status?: string;
  decision?: { allowed?: boolean; blockers?: string[]; warnings?: string[] };
  checks?: RuntimeCheck[];
};

export type ServerHealth = {
  process?: RuntimeProcess;
  serverHealth?: {
    metrics?: ServerRuntimeMetrics;
    checkedAt?: string;
    checks?: RuntimeCheck[];
    status?: string;
    summary?: string;
  };
};

export type ServerResources = {
  projects: Array<{ id: string; name: string }>;
  applications: Array<{ id: string; name: string; projectId?: string; status?: string }>;
};

export function fetchServerHealth(serverId: string) {
  return fetchJson<ServerHealth>(`servers/${serverId}/health`);
}

export function fetchServerResources(serverId: string) {
  return fetchJson<ServerResources>(`servers/${serverId}/resources`);
}

export type ServerRuntimeComponent = "docker" | "swarm" | "traefik" | "frps" | "jenkins";

export type ServerRuntimeProbe = {
  process?: RuntimeProcess;
  serverRuntime?: { metrics?: ServerRuntimeMetrics; checks?: RuntimeCheck[]; summary?: string };
};

export function probeServerRuntime(serverId: string) {
  return fetchJson<ServerRuntimeProbe>(`servers/${serverId}/runtime/probe`, { method: "POST" });
}

export type SwarmInstallInput = (
  | { mode: "manager" }
  | { mode: "worker"; managerServerId: string }
) & { advertiseAddr?: string; connectionMode?: "public" | "private" };

export function rebuildSwarm(
  serverId: string,
  input: { connectionMode: "public" | "private"; advertiseAddr: string },
  requestKey?: string,
) {
  return fetchJson<OperationRun>(`servers/${serverId}/swarm/rebuild`, {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: input,
  });
}

export function recoverSwarmRebuild(serverId: string, operationRunId: string, requestKey?: string) {
  return fetchJson<OperationRun>(`servers/${serverId}/swarm/rebuild/recover`, {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: { operationRunId },
  });
}

export function installServerComponent(
  serverId: string,
  component: ServerRuntimeComponent,
  input?: SwarmInstallInput,
  requestKey?: string,
) {
  return fetchJson<OperationRun>(`servers/${serverId}/${component}/install`, {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: component === "swarm" ? input : {},
  });
}

export function uninstallServerComponent(
  serverId: string,
  component: ServerRuntimeComponent,
  requestKey?: string,
) {
  return fetchJson<OperationRun>(`servers/${serverId}/${component}/uninstall`, {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: {},
  });
}

export function installMonitoringAgent(serverId: string, requestKey?: string) {
  return fetchJson<OperationRun>("monitoring/agents/install", {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: { serverId },
  });
}

export function uninstallMonitoringAgent(serverId: string, requestKey?: string) {
  return fetchJson<OperationRun>("monitoring/agents/uninstall", {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: { serverId },
  });
}

export type SwarmUninstallImpact = {
  blocked: boolean;
  errorCode?: string;
  reasons: Array<{
    code?: string;
    resourceKind?: string;
    resourceId?: string;
    resourceName?: string;
    field?: string;
    serverName?: string;
    applicationName?: string;
  }>;
};

export async function fetchSwarmUninstallImpact(serverId: string): Promise<SwarmUninstallImpact> {
  const result = await fetchJson<{ impact: SwarmUninstallImpact; errorCode?: string }>(
    `servers/${serverId}/swarm/uninstall-impact`,
  );
  return { ...result.impact, errorCode: result.errorCode };
}

export function fetchServerOperationLogs(serverId: string, operationId?: string, page = 1) {
  return fetchJson<Page<OperationLogEntry>>(
    `logs/runtime?targetKind=server&targetId=${encodeURIComponent(serverId)}${operationId ? `&operationRunId=${encodeURIComponent(operationId)}` : ""}&page=${page}&pageSize=100&order=desc`,
  );
}
