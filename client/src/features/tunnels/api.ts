import type { FrpTunnel, Page, Server } from "@/contracts/types";
import { fetchJson } from "@/shared/api/client";
import type { TaskResponse } from "@/shared/operations/task-response";
import type {
  FrpProbeHit,
  FrpProbeStatus,
  FrpTunnelDetail,
  FrpTunnelRun,
  TunnelDraft,
  TunnelPreview,
} from "./types";

/** 创建与启动返回同一组对象；`task` 是确认与取消的唯一标识。 */
export type TunnelMutationResult = {
  tunnel: FrpTunnel;
  run?: FrpTunnelRun;
  task: TaskResponse;
};

export function fetchTunnels() {
  return fetchJson<{ items: FrpTunnel[] }>("servers/tunnels");
}

export function fetchTunnel(tunnelId: string) {
  return fetchJson<FrpTunnelDetail>(`servers/tunnels/${tunnelId}`);
}

export function fetchTunnelServers() {
  return fetchJson<Page<Server>>("servers");
}

/** 编辑既有隧道时传 `tunnelId`，让预检排除自身名称。 */
export function previewTunnel(draft: TunnelDraft, tunnelId?: string) {
  const path = tunnelId
    ? `servers/tunnels/preview?tunnelId=${encodeURIComponent(tunnelId)}`
    : "servers/tunnels/preview";
  return fetchJson<TunnelPreview>(path, { method: "POST", body: draft });
}

export function createTunnel(draft: TunnelDraft, requestKey?: string) {
  return fetchJson<TunnelMutationResult>("servers/tunnels", {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: draft,
  });
}

export function updateTunnel(tunnelId: string, draft: TunnelDraft) {
  return fetchJson<FrpTunnel>(`servers/tunnels/${tunnelId}`, { method: "PUT", body: draft });
}

export function deleteTunnel(tunnelId: string, requestKey?: string) {
  return fetchJson<TaskResponse>(`servers/tunnels/${tunnelId}`, { method: "DELETE", requestKey });
}

export function startTunnel(tunnelId: string, requestKey?: string) {
  return fetchJson<TunnelMutationResult>(`servers/tunnels/${tunnelId}/start`, {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: {},
  });
}

export function stopTunnel(tunnelId: string, requestKey?: string) {
  return fetchJson<TaskResponse>(`servers/tunnels/${tunnelId}/stop`, {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: {},
  });
}

export function fetchTunnelRuns(tunnelId: string, page = 1, pageSize = 20) {
  return fetchJson<{ items: FrpTunnelRun[]; page: number; total: number }>(
    `servers/tunnels/${tunnelId}/runs?page=${page}&pageSize=${pageSize}`,
  );
}

export function fetchTunnelProbeHits(tunnelId: string, page = 1, pageSize = 50) {
  return fetchJson<{ items: FrpProbeHit[]; page: number; total: number; hitCount: number }>(
    `servers/tunnels/${tunnelId}/probe-hits?page=${page}&pageSize=${pageSize}`,
  );
}

export function clearTunnelProbeHits(tunnelId: string) {
  return fetchJson<{ ok: boolean; tunnelId: string }>(`servers/tunnels/${tunnelId}/probe-hits`, {
    method: "DELETE",
  });
}

export function fetchTunnelProbeStatus() {
  return fetchJson<FrpProbeStatus>("servers/tunnels/probe/status");
}
