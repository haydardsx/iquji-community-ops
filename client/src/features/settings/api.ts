import { z } from "zod";
import { fetchDownload, fetchJson } from "@/shared/api/client";
import type { TaskResponse } from "@/shared/operations/task-response";

const controlPlaneConfigSchema = z.object({
  hostname: z.string(),
  apiHost: z.string(),
  apiPort: z.number(),
  publicBaseUrl: z.string().optional(),
  dataDirectory: z.string(),
  version: z.string(),
});

const localConfigResponseSchema = z.object({
  controlPlane: controlPlaneConfigSchema,
  generatedAt: z.string().optional(),
});

export type LocalConfigResponse = z.infer<typeof localConfigResponseSchema>;

export async function fetchLocalConfig(): Promise<LocalConfigResponse> {
  const data = await fetchJson<unknown>("platform/local-config");
  return localConfigResponseSchema.parse(data);
}

export const backupScopeValues = [
  "projects",
  "servers",
  "domains_certificates",
  "secret_assets",
  "frp_tunnels",
] as const;

export const backupScopeSchema = z.enum(backupScopeValues);
export type BackupScope = z.infer<typeof backupScopeSchema>;

export function getBackupScopes(
  t?: (key: string) => string,
): Array<{ label: string; value: BackupScope }> {
  return [
    { label: t ? t("backup.scopeProjects") : "项目与应用", value: "projects" },
    { label: t ? t("backup.scopeServers") : "服务器", value: "servers" },
    { label: t ? t("backup.scopeDomainsCerts") : "域名与证书", value: "domains_certificates" },
    { label: t ? t("backup.scopeSecrets") : "密钥资产", value: "secret_assets" },
    { label: t ? t("backup.scopeTunnels") : "FRP 隧道", value: "frp_tunnels" },
  ];
}

export const backupScopes: Array<{ label: string; value: BackupScope }> = getBackupScopes();

export type BackupOptions = {
  requestedScopes: BackupScope[];
  includeSensitiveData: boolean;
  includeLogsAndAudit: boolean;
};

const backupDependencySchema = z.object({
  scope: backupScopeSchema,
  requiredByScope: backupScopeSchema,
  resource: z
    .object({
      kind: z.string(),
      id: z.string(),
      name: z.string().optional(),
    })
    .passthrough(),
  reason: z.string(),
});

export type BackupDependency = z.infer<typeof backupDependencySchema>;

const backupManifestSchema = z.object({
  applicationVersion: z.string(),
  schemaVersion: z.string(),
  requestedScopes: z
    .array(backupScopeSchema)
    .nullish()
    .transform((v) => v ?? []),
  effectiveScopes: z
    .array(backupScopeSchema)
    .nullish()
    .transform((v) => v ?? []),
  dependencyReasons: z
    .array(backupDependencySchema)
    .nullish()
    .transform((v) => v ?? []),
  recordCounts: z
    .record(z.string(), z.number())
    .nullish()
    .transform((v) => v ?? {}),
  includesSensitiveData: z.boolean(),
  includesLogsAndAudit: z.boolean(),
  payloadSha256: z.string(),
  createdAt: z.string(),
});

export type BackupManifest = z.infer<typeof backupManifestSchema>;

const backupPreviewSchema = z.object({
  manifest: backupManifestSchema,
  previewDigest: z.string(),
  availableScopes: z
    .array(backupScopeSchema)
    .nullish()
    .transform((v) => v ?? []),
  blockers: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
  warnings: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
});

export type BackupPreview = z.infer<typeof backupPreviewSchema>;

export async function previewBackup(options: BackupOptions): Promise<BackupPreview> {
  const data = await fetchJson<unknown>("backup-restore/backups/preview", {
    method: "POST",
    body: { options },
  });
  return backupPreviewSchema.parse(data);
}

export function downloadBackup(input: {
  options: BackupOptions;
  previewDigest: string;
  passphrase: string;
}) {
  return fetchDownload("backup-restore/backups/download", { method: "POST", body: input });
}

const restoreUploadSchema = z.object({
  uploadId: z.string(),
  expiresAt: z.string(),
  manifest: backupManifestSchema,
});

export type RestoreUpload = z.infer<typeof restoreUploadSchema>;

export async function uploadRestore(file: File, passphrase: string): Promise<RestoreUpload> {
  const form = new FormData();
  form.set("file", file);
  form.set("passphrase", passphrase);
  const data = await fetchJson<unknown>("backup-restore/restores/uploads", {
    method: "POST",
    form,
  });
  return restoreUploadSchema.parse(data);
}

const restoreConflictSchema = z
  .object({
    id: z.string(),
    kind: z.string(),
    sourceId: z.string(),
    destinationId: z.string().optional(),
    naturalKey: z.string(),
    reasonCodes: z
      .array(z.string())
      .nullish()
      .transform((v) => v ?? []),
    summary: z.string(),
  })
  .passthrough();

export type RestoreConflict = z.infer<typeof restoreConflictSchema>;

const restorePreviewSchema = z.object({
  uploadId: z.string(),
  previewDigest: z.string(),
  stateDigest: z.string(),
  manifest: backupManifestSchema,
  availableScopes: z
    .array(backupScopeSchema)
    .nullish()
    .transform((v) => v ?? []),
  selectedScopes: z
    .array(backupScopeSchema)
    .nullish()
    .transform((v) => v ?? []),
  dependencyReasons: z
    .array(backupDependencySchema)
    .nullish()
    .transform((v) => v ?? []),
  conflicts: z
    .array(restoreConflictSchema)
    .nullish()
    .transform((v) => v ?? []),
  missingSecretRefs: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
  blockers: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
  warnings: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
});

export type RestorePreview = z.infer<typeof restorePreviewSchema>;

export async function previewRestore(input: {
  uploadId: string;
  options: {
    requestedScopes: BackupScope[];
    includeLogsAndAudit: boolean;
    conflictPolicy: "abort_on_conflict" | "overwrite_selected";
  };
}): Promise<RestorePreview> {
  const data = await fetchJson<unknown>("backup-restore/restores/preview", {
    method: "POST",
    body: input,
  });
  return restorePreviewSchema.parse(data);
}

export function executeRestore(
  input: {
    uploadId: string;
    options: {
      requestedScopes: BackupScope[];
      includeLogsAndAudit: boolean;
      conflictPolicy: "abort_on_conflict" | "overwrite_selected";
    };
    previewDigest: string;
  },
  requestKey?: string,
) {
  return fetchJson<TaskResponse>("backup-restore/restores/execute", {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: input,
  });
}

export type DataMaintenanceAction = "clear_logs" | "factory_reset";

const dataMaintenancePreviewSchema = z.object({
  action: z.enum(["clear_logs", "factory_reset"]),
  confirmationToken: z.string(),
  confirmationPhrase: z.string(),
  stateDigest: z.string(),
  expiresAt: z.string(),
  deleteCounts: z
    .record(z.string(), z.number())
    .nullish()
    .transform((v) => v ?? {}),
  preserveCounts: z
    .record(z.string(), z.number())
    .nullish()
    .transform((v) => v ?? {}),
  blockers: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
  warnings: z
    .array(z.string())
    .nullish()
    .transform((v) => v ?? []),
});

export type DataMaintenancePreview = z.infer<typeof dataMaintenancePreviewSchema>;

const dataMaintenanceResultSchema = z
  .object({
    action: z.enum(["clear_logs", "factory_reset"]),
    status: z.string(),
    clearedCounts: z
      .record(z.string(), z.number())
      .nullish()
      .transform((v) => v ?? {}),
    completedAt: z.string(),
  })
  .passthrough();

export async function previewDataMaintenance(
  action: DataMaintenanceAction,
): Promise<DataMaintenancePreview> {
  const data = await fetchJson<unknown>(
    `operations/data-maintenance/${action === "clear_logs" ? "clear-logs" : "factory-reset"}/preview`,
    { method: "POST" },
  );
  return dataMaintenancePreviewSchema.parse(data);
}

export async function executeDataMaintenance(
  action: DataMaintenanceAction,
  input: { confirmationToken: string; stateDigest: string; confirmationPhrase: string },
) {
  const data = await fetchJson<unknown>(
    `operations/data-maintenance/${action === "clear_logs" ? "clear-logs" : "factory-reset"}/execute`,
    { method: "POST", body: input },
  );
  return dataMaintenanceResultSchema.parse(data);
}
