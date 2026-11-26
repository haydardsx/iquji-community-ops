import type { SecretAsset, Server } from "@/contracts/types";

export type SecretTab = "all" | "git" | "ssh" | "application" | "registry";

export type SecretDeletePreview = {
  usage: {
    servers: string[];
    gitApplications: string[];
    registryApplications?: string[];
    applicationVariables: string[];
  };
  blocked: boolean;
};

/** 凭据类型与用途由类型派生，界面不再单独暴露用途选择。 */
export function derivePurpose(assetType: string): string {
  if (assetType === "registry_secret") return "registry_push";
  return assetType === "application_secret"
    ? "application_variables"
    : assetType === "git_token"
      ? "git_auth"
      : "deployment_server";
}

export function assetTypeLabel(assetType: string, t?: (key: any) => string): string {
  if (assetType === "registry_secret") return t ? t("secrets.typeRegistrySecret") : "镜像仓库凭据";
  if (assetType === "git_token") return t ? t("secrets.typeGitToken") : "Git 凭据";
  if (assetType === "application_secret")
    return t ? t("secrets.typeApplicationSecret") : "应用凭据";
  return t ? t("secrets.typeSshKey") : "SSH 私钥";
}

export function assetTypeTone(assetType: string): "info" | "success" | "warning" {
  if (assetType === "git_token") return "info";
  if (assetType === "application_secret") return "warning";
  return "success";
}

export function purposeLabel(
  assetType: string,
  purpose?: string,
  t?: (key: any) => string,
): string {
  if (assetType === "registry_secret") return t ? t("secrets.registryPurpose") : "镜像仓库推送认证";
  if (assetType === "git_token") return t ? t("secrets.purposeGit") : "代码仓库拉取认证";
  if (assetType === "application_secret")
    return t ? t("secrets.purposeApplicationSecret") : "应用变量引用";
  return purpose === "git_auth"
    ? t
      ? t("secrets.purposeGitSsh")
      : "Git over SSH 认证"
    : t
      ? t("secrets.purposeSsh")
      : "服务器登录与部署";
}

export function materialLabel(materialStatus?: string, t?: (key: any) => string): string {
  if (materialStatus === "pending") return t ? t("secrets.materialPending") : "等待执行";
  return materialStatus === "available"
    ? t
      ? t("secrets.materialAvailable")
      : "材料就绪"
    : t
      ? t("secrets.materialMissing")
      : "材料缺失";
}

export function materialTone(materialStatus?: string): "success" | "warning" {
  return materialStatus === "available" ? "success" : "warning";
}

/** 关联服务器名称，按服务器绑定关系反查，不依赖凭据自身字段。 */
export function linkedServers(assetId: string, servers: Server[]): string[] {
  return servers
    .filter((server) => server.secretAssetIds?.includes(assetId))
    .map((server) => server.name);
}

/** 可作为部署服务器 SSH 凭据绑定的资产：SSH 私钥 + 材料就绪。 */
export function bindableSshAssets(assets: SecretAsset[]): SecretAsset[] {
  return assets.filter(
    (asset) => asset.assetType === "ssh_key" && asset.materialStatus !== "pending",
  );
}

export function filterByTab(assets: SecretAsset[], tab: SecretTab): SecretAsset[] {
  if (tab === "all") return assets;
  if (tab === "registry") return assets.filter((asset) => asset.assetType === "registry_secret");
  if (tab === "application")
    return assets.filter((asset) => asset.assetType === "application_secret");
  const target = tab === "git" ? "git_token" : "ssh_key";
  return assets.filter((asset) => asset.assetType === target);
}

export function filterByQuery(
  assets: SecretAsset[],
  query: string,
  servers: Server[],
): SecretAsset[] {
  const keyword = query.trim().toLowerCase();
  if (!keyword) return assets;
  return assets.filter((asset) => {
    const haystack = [
      asset.name,
      asset.description ?? "",
      asset.secretRef ?? "",
      asset.maskedSummary ?? "",
      ...linkedServers(asset.id, servers),
    ]
      .join(" ")
      .toLowerCase();
    return haystack.includes(keyword);
  });
}

export function countByTab(assets: SecretAsset[]): Record<SecretTab, number> {
  return {
    all: assets.length,
    registry: assets.filter((asset) => asset.assetType === "registry_secret").length,
    git: assets.filter((asset) => asset.assetType === "git_token").length,
    ssh: assets.filter((asset) => asset.assetType === "ssh_key").length,
    application: assets.filter((asset) => asset.assetType === "application_secret").length,
  };
}

/**
 * 删除确认文案：先说明将自动解除的引用，再列出阻断项。
 */
export function describeDeleteImpact(
  preview: SecretDeletePreview,
  name: string,
  t?: (key: string, params?: Record<string, string | number>) => string,
): string {
  const lines: string[] = [];
  const separator = t ? ", " : "、";
  if (preview.usage.servers.length > 0) {
    lines.push(
      t
        ? t("secrets.deleteImpactServers", {
            count: preview.usage.servers.length,
            servers: preview.usage.servers.join(separator),
          })
        : `将从 ${preview.usage.servers.length} 台服务器解绑：${preview.usage.servers.join("、")}`,
    );
  }
  if (preview.usage.gitApplications.length > 0) {
    lines.push(
      t
        ? t("secrets.deleteImpactGit", {
            count: preview.usage.gitApplications.length,
            apps: preview.usage.gitApplications.join(separator),
          })
        : `将清除 ${preview.usage.gitApplications.length} 个应用的 Git 认证：${preview.usage.gitApplications.join("、")}`,
    );
  }
  if (preview.usage.registryApplications?.length) {
    const apps = preview.usage.registryApplications.join(separator);
    lines.push(t ? t("secrets.registryUsage", { apps }) : `镜像推送仍引用该凭据：${apps}`);
  }
  if (preview.usage.applicationVariables.length > 0) {
    lines.push(
      t
        ? t("secrets.deleteImpactVars", {
            vars: preview.usage.applicationVariables.join(separator),
          })
        : `应用变量仍引用该凭据：${preview.usage.applicationVariables.join("、")}`,
    );
  }
  if (lines.length === 0) {
    lines.push(
      t ? t("secrets.deleteImpactNone") : "当前没有服务器、Git 认证或环境配置引用该凭据。",
    );
  }
  const head = preview.blocked
    ? t
      ? t("secrets.deleteImpactBlocked", { name })
      : `「${name}」仍被运行配置引用，请先解除引用后再删除。`
    : t
      ? t("secrets.deleteImpactDesc", { name })
      : `删除「${name}」后不可恢复，受控存储中的密钥材料会同步清除。`;
  return [head, ...lines].join("\n");
}
