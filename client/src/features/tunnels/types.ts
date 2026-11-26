import type { Id, Timestamp } from "@/contracts/types";

export type FrpTunnelMappingTarget = "local_service" | "http_probe";

export type FrpTunnelMapping = {
  name: string;
  targetType: FrpTunnelMappingTarget;
  localHost: string;
  localPort: number;
  frpsProxyPort: number;
  publicDomain?: string;
  publicPathPrefix: string;
  entrypoint: string;
  publicScheme: string;
  publicUrl?: string;
};

export type FrpTunnelRun = {
  id: string;
  tunnelId: string;
  serverId?: string;
  status: string;
  legacyIngress?: boolean;
  frpsBindPort?: number;
  containerName?: string;
  remoteDir?: string;
  startOperationRunId?: string;
  stopOperationRunId?: string;
  errorSummary?: string;
  startedAt?: string;
  stoppedAt?: string;
  failedAt?: string;
  lastVerifiedAt?: string;
  createdAt: string;
  updatedAt: string;
};

export type FrpProbeHit = {
  level: "info";
  source: "probe";
  event: "probe.request";
  message: string;
  timestamp: string;
  id: string;
  tunnelId: string;
  runId?: string;
  mappingName: string;
  method: string;
  host?: string;
  path: string;
  rawQuery?: string;
  remoteAddr: string;
  userAgent?: string;
  receivedAt: string;
};

export type TunnelServerSummary = {
  id: string;
  name: string;
  publicHost?: string;
};

export type FrpTunnelDetail = {
  id: Id;
  name: string;
  serverId: Id;
  frpsBindPort: number;
  mappings: FrpTunnelMapping[];
  status?: string;
  runCount?: number;
  probeHitCount?: number;
  lastRunAt?: string;
  createdAt: Timestamp;
  updatedAt: Timestamp;
  server?: TunnelServerSummary;
  latestRun?: FrpTunnelRun;
};

export type TunnelStatusTone = "neutral" | "info" | "success" | "warning" | "danger";

export type TunnelMappingInput = {
  name: string;
  targetType: "local_service" | "http_probe";
  localHost: string;
  localPort: number;
  frpsProxyPort: number;
  publicDomain: string;
  publicPathPrefix: string;
  entrypoint: string;
  publicScheme: string;
};

export type TunnelDraft = {
  name: string;
  serverId: Id;
  frpsBindPort: number;
  mappings: TunnelMappingInput[];
};

/** `POST servers/tunnels/preview` 的预检结果，只取前端展示所需字段。 */
export type TunnelPreview = {
  process?: {
    status?: string;
    decision?: { allowed?: boolean; blockers?: string[]; warnings?: string[] };
    checks?: { id: string; outcome: string; details?: { capability?: string } }[];
  };
  /** 预检payload 挂在 `tunnelPreview` 下，包含当前映射会生成的公网入口。 */
  tunnelPreview?: { endpoints?: string[] };
};

export type FrpProbeStatus = {
  enabled: boolean;
  listenHost: string;
  listenPort: number;
  localEndpoint: string;
  startedAt?: string;
  errorSummary?: string;
};
