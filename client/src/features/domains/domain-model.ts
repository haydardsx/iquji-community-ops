import type { Certificate, Domain, Server } from "@/contracts/types";

/** 证书状态只反映网关真实签发校验结果，保存资产本身不代表已签发。 */
export function certificateStatusLabel(
  certificate: Pick<Certificate, "status">,
  t?: (key: any) => string,
): string {
  switch (certificate.status) {
    case "valid":
      return t ? t("domains.statusValid") : "已验证";
    case "failed":
      return t ? t("domains.statusFailed") : "签发失败";
    default:
      return t ? t("domains.statusPending") : "待签发";
  }
}

export function certificateStatusTone(
  certificate: Pick<Certificate, "status">,
): "success" | "danger" | "warning" {
  switch (certificate.status) {
    case "valid":
      return "success";
    case "failed":
      return "danger";
    default:
      return "warning";
  }
}

export function certificateExpiryLabel(
  expiresAt?: string,
  now = new Date(),
  t?: (key: any, values?: any) => string,
  locale = "zh-CN",
): string {
  if (!expiresAt) return t ? t("domains.noExpiry") : "未记录到期时间";
  const date = new Date(expiresAt);
  if (Number.isNaN(date.getTime())) return t ? t("domains.noExpiry") : "未记录到期时间";
  const days = Math.floor((date.getTime() - now.getTime()) / 86_400_000);
  if (days < 0) {
    const expiredPrefix = t ? t("domains.expired") : "已过期";
    return `${expiredPrefix} · ${date.toLocaleDateString(locale)}`;
  }
  if (days === 0) return t ? t("domains.expiresToday") : "今天到期";
  return t ? t("domains.expiresInDays", { days }) : `${days} 天后到期`;
}

/** 域名 HTTPS 只由绑定证书的状态决定，避免页面出现第二套真相。 */
export function domainHttpsLabel(
  domain: Pick<Domain, "certificateId" | "redirectRuntime">,
  certificate?: Pick<Certificate, "status">,
  t?: (key: any) => string,
): { label: string; tone: "neutral" | "success" | "warning" } {
  if (!domain.certificateId) return { label: "HTTP", tone: "neutral" };
  if (!certificate || certificate.status !== "valid") {
    return { label: t ? t("domains.statusPending") : "待签发", tone: "warning" };
  }
  return {
    label:
      domain.redirectRuntime?.status === "active"
        ? t
          ? t("domains.httpsRedirect")
          : "已启用 · 自动跳转"
        : t
          ? t("domains.statusValid")
          : "已启用",
    tone: "success",
  };
}

/** 只有已安装 Traefik 的主机能承担 HTTP-01 验证，避免选择不可用目标。 */
export function traefikGateways(servers: Server[]): Server[] {
  return servers.filter((server) => {
    const metrics = (server.health?.metrics ?? {}) as { traefik?: boolean };
    return metrics.traefik === true;
  });
}

export function domainHostOptions(domains: Domain[]) {
  return domains.map((domain) => ({ label: domain.name, value: domain.id }));
}

export type DomainTreeNode = {
  domain: Domain;
  depth: number;
  isRoot: boolean;
  hasChildren: boolean;
  isLastChild: boolean;
  childCount: number;
  parent?: Domain;
  subdomainPart?: string;
  children: DomainTreeNode[];
};

/** 拆分域名前缀部分与父级后缀部分，便于在 UI 上加粗展示子域名前缀。 */
export function splitDomainPrefix(
  name: string,
  parentName?: string,
): { prefix: string; parentSuffix: string } {
  if (parentName && name.toLowerCase().endsWith(`.${parentName.toLowerCase()}`)) {
    const prefix = name.slice(0, -(parentName.length + 1));
    return { prefix, parentSuffix: `.${parentName}` };
  }
  return { prefix: name, parentSuffix: "" };
}

/**
 * 构建域名层级树：
 * 1. 优先使用显式设置的 parentDomainId。
 * 2. 未设置时，自动寻找满足最长后缀匹配的父级域名（如 demo-web.example.com 自动匹配 example.com）。
 * 3. 严格检测并防止环形引用。
 * 4. 根节点与子节点均按字母序稳定排序。
 */
export function buildDomainTree(domains: Domain[]): DomainTreeNode[] {
  const domainById = new Map<string, Domain>();
  for (const item of domains) {
    domainById.set(item.id, item);
  }

  // 确定每个域名的直接父级 ID
  const directParentIdByChildId = new Map<string, string>();
  for (const item of domains) {
    // 1. 显式父级优先
    if (
      item.parentDomainId &&
      domainById.has(item.parentDomainId) &&
      item.parentDomainId !== item.id
    ) {
      directParentIdByChildId.set(item.id, item.parentDomainId);
      continue;
    }
    // 2. 自动最长后缀匹配
    let bestParent: Domain | null = null;
    const lowerName = item.name.toLowerCase();
    for (const candidate of domains) {
      if (candidate.id === item.id) continue;
      const suffix = `.${candidate.name.toLowerCase()}`;
      if (lowerName.endsWith(suffix)) {
        if (!bestParent || candidate.name.length > bestParent.name.length) {
          bestParent = candidate;
        }
      }
    }
    if (bestParent) {
      directParentIdByChildId.set(item.id, bestParent.id);
    }
  }

  // 环路检测：如果沿父级链路能回到自身，则断开环
  for (const childId of Array.from(directParentIdByChildId.keys())) {
    const visited = new Set<string>([childId]);
    let curr = directParentIdByChildId.get(childId);
    while (curr) {
      if (visited.has(curr)) {
        directParentIdByChildId.delete(childId);
        break;
      }
      visited.add(curr);
      curr = directParentIdByChildId.get(curr);
    }
  }

  // 构建子节点列表
  const childrenMap = new Map<string, Domain[]>();
  const rootDomains: Domain[] = [];

  for (const item of domains) {
    const parentId = directParentIdByChildId.get(item.id);
    if (parentId) {
      const list = childrenMap.get(parentId) ?? [];
      list.push(item);
      childrenMap.set(parentId, list);
    } else {
      rootDomains.push(item);
    }
  }

  function buildSubtree(
    domain: Domain,
    depth: number,
    isLastChild: boolean,
    parent?: Domain,
  ): DomainTreeNode {
    const rawChildren = childrenMap.get(domain.id) ?? [];
    rawChildren.sort((a, b) => a.name.localeCompare(b.name));

    const children: DomainTreeNode[] = [];
    for (let i = 0; i < rawChildren.length; i++) {
      const isLast = i === rawChildren.length - 1;
      children.push(buildSubtree(rawChildren[i], depth + 1, isLast, domain));
    }

    const { prefix } = splitDomainPrefix(domain.name, parent?.name);

    return {
      domain,
      depth,
      isRoot: depth === 0,
      hasChildren: children.length > 0,
      isLastChild,
      childCount: children.length,
      parent,
      subdomainPart: prefix,
      children,
    };
  }

  rootDomains.sort((a, b) => a.name.localeCompare(b.name));
  return rootDomains.map((root, index) =>
    buildSubtree(root, 0, index === rootDomains.length - 1, undefined),
  );
}

/**
 * 将树形结构拍平为可直接传给表格的有序一维列表。
 * 若节点处于折叠集合 collapsedIds 中，则跳过其子节点。
 */
export function flattenDomainTree(
  nodes: DomainTreeNode[],
  collapsedIds: Set<string>,
): DomainTreeNode[] {
  const result: DomainTreeNode[] = [];

  function traverse(node: DomainTreeNode) {
    result.push(node);
    if (!collapsedIds.has(node.domain.id)) {
      for (const child of node.children) {
        traverse(child);
      }
    }
  }

  for (const node of nodes) {
    traverse(node);
  }

  return result;
}
