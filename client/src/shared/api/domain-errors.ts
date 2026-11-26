import { z } from "zod";

const redirectUsage = z.object({
  affectedRedirects: z.array(
    z.object({
      sourceId: z.string(),
      sourceName: z.string().optional(),
      targetId: z.string(),
      targetName: z.string().optional(),
    }),
  ),
});

export function domainRedirectImpact(value: unknown): string[] {
  const parsed = redirectUsage.safeParse(value);
  if (!parsed.success || !parsed.data.affectedRedirects.length) return [];
  const names = parsed.data.affectedRedirects.map(
    (rule) => `${rule.sourceName || rule.sourceId} → ${rule.targetName || rule.targetId}`,
  );
  return [`${domainErrors.DOMAIN_REDIRECT_TARGET_IN_USE} 受影响规则：${names.join("、")}`];
}

export const domainErrors: Record<string, string> = {
  DOMAIN_ASSET_HAS_ACTIVE_ROUTE: "该资产仍被生效入口引用，请先停用入口。",
  DOMAIN_ASSET_NAME_CONFLICT: "同类资产已存在该名称。",
  DOMAIN_NAME_INVALID: "请填写完整域名，不包含协议、路径或通配符。",
  DOMAIN_CERTIFICATE_INVALID: "所选证书未包含该域名或不属于受管签发方式。",
  DOMAIN_HAS_ROUTE: "域名仍被应用入口引用，请先删除入口。",
  DOMAIN_REDIRECT_TARGET_INVALID: "目标域名不存在，请重新选择已登记的域名。",
  DOMAIN_REDIRECT_GATEWAY_REQUIRED: "请选择已安装 Traefik 的网关。",
  DOMAIN_REDIRECT_CYCLE: "所选目标会形成循环跳转。",
  DOMAIN_REDIRECT_UPGRADE_REQUIRES_HTTPS: "跳转到同一域名时，请绑定源域名证书并选择 HTTPS。",
  DOMAIN_REDIRECT_HOST_IN_USE: "该域名已有冲突的应用、跳转或穿透入口，请先撤销对应入口。",
  DOMAIN_REDIRECT_TARGET_IN_USE: "此域名仍被跳转规则引用，请先修改或移除相关规则。",
  DOMAIN_REDIRECT_DRAFT_CHANGED: "域名或规则已变更，请刷新后重新发布。",
  DOMAIN_REDIRECT_RESPONSE_INVALID: "网关返回的状态码或目标地址与发布规则不一致。",
};
