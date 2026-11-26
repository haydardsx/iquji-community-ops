import type { OperationRun, SecretAsset } from "@/contracts/types";
import type { SecretDeletePreview } from "@/features/secrets/secrets-model";
import { fetchJson } from "@/shared/api/client";

export const SECRET_ASSETS_QUERY_KEY = ["secrets", "assets"] as const;

export function fetchSecretAssets() {
  return fetchJson<{ items: SecretAsset[] }>("secrets/assets");
}

export function createSecretAsset(
  input: {
    name: string;
    assetType: string;
    description?: string;
    secretValue: string;
    bindServerId?: string;
  },
  requestKey?: string,
) {
  return fetchJson<{ asset: SecretAsset; operation?: OperationRun }>("secrets/assets", {
    method: "POST",
    ...(requestKey ? { requestKey } : {}),
    body: {
      ...input,
    },
  });
}

export function updateSecretAsset(assetId: string, input: { name?: string; description?: string }) {
  return fetchJson<SecretAsset>(`secrets/assets/${assetId}`, { method: "PUT", body: input });
}

export function deleteSecretAsset(assetId: string) {
  return fetchJson<{ ok: boolean }>(`secrets/assets/${assetId}`, { method: "DELETE" });
}

export function rotateSecretAsset(assetId: string, secretValue: string, requestKey?: string) {
  return fetchJson<{ asset: SecretAsset; operation?: OperationRun }>(
    `secrets/assets/${assetId}/secret`,
    { method: "PUT", ...(requestKey ? { requestKey } : {}), body: { secretValue } },
  );
}

export function bindSecretToServer(input: { serverId: string; secretAssetIds: string[] }) {
  return fetchJson<Record<string, unknown>>(`servers/${input.serverId}/secret-assets`, {
    method: "PUT",
    body: { secretAssetIds: input.secretAssetIds },
  });
}

type DeletePreviewResponse = {
  deleteImpact?: {
    blocked?: boolean;
    secretUsage?: {
      servers?: string[];
      gitApplications?: string[];
      registryApplications?: string[];
      applicationVariables?: string[];
    };
  };
};

/** 读取删除前的影响面，用于确认弹窗说明级联解绑与阻断原因。 */
export async function fetchSecretDeletePreview(assetId: string): Promise<SecretDeletePreview> {
  const response = await fetchJson<DeletePreviewResponse>(
    `operations/delete-preview?targetKind=secret_asset&targetId=${encodeURIComponent(assetId)}`,
  );
  const usage = response.deleteImpact?.secretUsage ?? {};
  return {
    blocked: response.deleteImpact?.blocked === true,
    usage: {
      servers: usage.servers ?? [],
      gitApplications: usage.gitApplications ?? [],
      registryApplications: usage.registryApplications,
      applicationVariables: usage.applicationVariables ?? [],
    },
  };
}
