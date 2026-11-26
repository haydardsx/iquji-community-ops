import type { QueryClient } from "@tanstack/react-query";
import { operationErrorMessage } from "@/shared/api/client";
import type { TranslateFn } from "@/shared/i18n";
import type { BackupDependency, BackupScope } from "./api";

const LOCAL_PREFERENCE_KEYS = [
  "iquops.theme",
  "iquops.language",
  "iquops.sidebarCollapsed",
] as const;

/** 将备份业务域标识映射为可读名称 */
export function formatScopeLabel(scope: BackupScope, t: TranslateFn): string {
  const keyMap: Record<BackupScope, string> = {
    projects: "backup.scopeProjects",
    servers: "backup.scopeServers",
    domains_certificates: "backup.scopeDomainsCerts",
    secret_assets: "backup.scopeSecrets",
    frp_tunnels: "backup.scopeTunnels",
  };
  return t(keyMap[scope]);
}

/** 将统计字典键名映射为可读实体类别名称 */
export function formatRecordCountLabel(key: string, t: TranslateFn): string {
  const keyMap: Record<string, string> = {
    projects: "backup.countProjects",
    applicationTemplates: "backup.countTemplates",
    applications: "backup.countApplications",
    applicationDeploymentConfigs: "backup.countDeploymentConfigs",
    routes: "backup.countRoutes",
    uploadFiles: "backup.countUploadAssets",
    uploadAssets: "backup.countUploadAssets",
    servers: "backup.countServers",
    serverRoles: "backup.countServerRoles",
    domains: "backup.countDomains",
    certificates: "backup.countCertificates",
    resolvers: "backup.countResolvers",
    ingresses: "backup.countIngresses",
    secretAssets: "backup.countSecrets",
    frpTunnels: "backup.countTunnels",
    runtimeLogs: "backup.countRuntimeLogs",
    operationLogs: "backup.countOperationLogs",
    secretMaterials: "backup.countSecretMaterials",
    releasePlans: "backup.countReleasePlans",
    applicationInstances: "backup.countInstances",
    monitoringAgents: "backup.countAgents",
    monitoringAgentRegistrations: "backup.countAgentRegs",
    runtimeLogStreams: "backup.countLogStreams",
    restoreStagingFiles: "backup.countRestoreStaging",
    backupStagingFiles: "backup.countBackupStaging",
    frpRuntimeFiles: "backup.countFrpFiles",
  };
  return keyMap[key] ? t(keyMap[key]) : key;
}

/** 将依赖自动拉起记录转换为中文/双语说明 */
export function formatDependencySummary(dep: BackupDependency, t: TranslateFn): string {
  const reasonMap: Record<string, string> = {
    PROJECT_SERVER_REFERENCE: "backup.depReasonProjectServer",
    PROJECT_ROUTE_DOMAIN_REFERENCE: "backup.depReasonProjectDomain",
    PROJECT_SECRET_REFERENCE: "backup.depReasonProjectSecret",
    SERVER_SECRET_BINDING: "backup.depReasonServerSecret",
    INGRESS_SERVER_REFERENCE: "backup.depReasonIngressServer",
    DOMAIN_REDIRECT_GATEWAY_REFERENCE: "backup.depReasonDomainGateway",
    CERTIFICATE_SECRET_REFERENCE: "backup.depReasonCertSecret",
    FRP_TUNNEL_SERVER_REFERENCE: "backup.depReasonTunnelServer",
  };
  const reasonText = reasonMap[dep.reason]
    ? t(reasonMap[dep.reason])
    : t("backup.depReasonGeneric");
  const resourceName = dep.resource.name || dep.resource.id;
  return `${formatScopeLabel(dep.requiredByScope, t)} → ${formatScopeLabel(dep.scope, t)}（${resourceName}：${reasonText}）`;
}

/** 将后端返回的阻塞项或警告码列表翻译为可读提示 */
export function formatDiagnosticCodes(codes: string[], t: TranslateFn): string[] {
  return codes.map((code) => operationErrorMessage(code, undefined, t).replace(/。$/u, ""));
}

/** 格式化备份归档时间 */
export function formatArchiveTimestamp(value?: string): string {
  if (!value) return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}

/** 提取非零计数字段，若全部为零则返回非空预览提示 */
export function nonZeroCountEntries(counts: Record<string, number>): Array<[string, number]> {
  return Object.entries(counts).filter(([, value]) => value > 0);
}

/** 出厂重置成功后清理本地 UI 偏好与查询缓存 */
export function resetClientAfterFactoryReset(queryClient: QueryClient) {
  for (const key of LOCAL_PREFERENCE_KEYS) {
    try {
      window.localStorage.removeItem(key);
    } catch {
      // 忽略受限存储环境
    }
  }
  queryClient.clear();
}
