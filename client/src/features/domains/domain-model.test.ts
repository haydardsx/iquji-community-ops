import { describe, expect, it } from "vitest";
import type { Certificate, Domain, Server } from "@/contracts/types";
import {
  buildDomainTree,
  certificateExpiryLabel,
  certificateStatusLabel,
  certificateStatusTone,
  domainHttpsLabel,
  flattenDomainTree,
  splitDomainPrefix,
  traefikGateways,
} from "./domain-model";

const domain = (patch: Partial<Domain>): Domain => ({
  id: "domain_1",
  name: "app.example.com",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...patch,
});

const gateway = (patch: Partial<Server>): Server => ({
  id: "srv_1",
  name: "网关",
  publicHost: "203.0.113.10",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...patch,
});

describe("域名与证书展示规则", () => {
  it("证书状态按真实签发结果派生", () => {
    expect(certificateStatusLabel({ status: "valid" })).toBe("已验证");
    expect(certificateStatusLabel({ status: "failed" })).toBe("签发失败");
    expect(certificateStatusLabel({})).toBe("待签发");
    expect(certificateStatusTone({ status: "valid" })).toBe("success");
    expect(certificateStatusTone({ status: "failed" })).toBe("danger");
    expect(certificateStatusTone({})).toBe("warning");
  });

  it("到期时间按天展示并对过期给出明确提示", () => {
    const now = new Date("2026-05-24T12:00:00Z");
    expect(certificateExpiryLabel(undefined, now)).toBe("未记录到期时间");
    expect(certificateExpiryLabel("not-a-date", now)).toBe("未记录到期时间");
    expect(certificateExpiryLabel("2026-05-20T12:00:00Z", now)).toContain("已过期");
    expect(certificateExpiryLabel("2026-05-24T18:00:00Z", now)).toBe("今天到期");
    expect(certificateExpiryLabel("2026-05-27T12:00:00Z", now)).toBe("3 天后到期");
  });

  it("只有绑定且已签发的证书才宣称 HTTPS", () => {
    expect(domainHttpsLabel(domain({})).label).toBe("HTTP");
    expect(domainHttpsLabel(domain({ certificateId: "cert_1" }), { status: "pending" }).label).toBe(
      "待签发",
    );
    expect(
      domainHttpsLabel(domain({ certificateId: "cert_1", redirectRuntime: { status: "active" } }), {
        status: "valid",
      }),
    ).toEqual({ label: "已启用 · 自动跳转", tone: "success" });
  });

  it("网关候选只包含已就绪 Traefik 的主机", () => {
    const servers = [
      gateway({ id: "srv_ready", health: { metrics: { traefik: true } } }),
      gateway({ id: "srv_missing", health: { metrics: { traefik: false } } }),
      gateway({ id: "srv_unknown" }),
    ];
    expect(traefikGateways(servers).map((server) => server.id)).toEqual(["srv_ready"]);
  });

  it("证书记录没有网关字段时不影响状态展示", () => {
    const certificate: Certificate = {
      id: "cert_1",
      name: "主站证书",
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
    };
    expect(certificateStatusLabel(certificate)).toBe("待签发");
  });

  describe("域名层级树构建与拍平规则", () => {
    it("按自然后缀匹配将子域名组织到主域名下", () => {
      const items: Domain[] = [
        domain({ id: "d_root", name: "example.com" }),
        domain({ id: "d_sub1", name: "demo-server1.example.com" }),
        domain({ id: "d_sub2", name: "demo-server2.example.com" }),
        domain({ id: "d_sub3", name: "demo-web.example.com" }),
      ];

      const tree = buildDomainTree(items);
      expect(tree).toHaveLength(1);
      expect(tree[0].domain.name).toBe("example.com");
      expect(tree[0].isRoot).toBe(true);
      expect(tree[0].depth).toBe(0);
      expect(tree[0].hasChildren).toBe(true);
      expect(tree[0].childCount).toBe(3);

      expect(tree[0].children.map((c) => c.domain.name)).toEqual([
        "demo-server1.example.com",
        "demo-server2.example.com",
        "demo-web.example.com",
      ]);
      expect(tree[0].children[0].depth).toBe(1);
      expect(tree[0].children[0].subdomainPart).toBe("demo-server1");
      expect(tree[0].children[2].isLastChild).toBe(true);
    });

    it("支持多级子域名嵌套", () => {
      const items: Domain[] = [
        domain({ id: "d1", name: "example.com" }),
        domain({ id: "d2", name: "api.example.com" }),
        domain({ id: "d3", name: "v1.api.example.com" }),
      ];

      const tree = buildDomainTree(items);
      expect(tree).toHaveLength(1);
      expect(tree[0].domain.name).toBe("example.com");
      expect(tree[0].children[0].domain.name).toBe("api.example.com");
      expect(tree[0].children[0].children[0].domain.name).toBe("v1.api.example.com");
      expect(tree[0].children[0].children[0].depth).toBe(2);
      expect(tree[0].children[0].children[0].subdomainPart).toBe("v1");
    });

    it("显式 parentDomainId 优先于后缀匹配", () => {
      const items: Domain[] = [
        domain({ id: "d_group", name: "custom-group" }),
        domain({ id: "d_item", name: "app.example.com", parentDomainId: "d_group" }),
      ];

      const tree = buildDomainTree(items);
      expect(tree).toHaveLength(1);
      expect(tree[0].domain.id).toBe("d_group");
      expect(tree[0].children[0].domain.id).toBe("d_item");
    });

    it("环形引用保护避免死循环", () => {
      const items: Domain[] = [
        domain({ id: "d1", name: "a.example.test", parentDomainId: "d2" }),
        domain({ id: "d2", name: "b.example.test", parentDomainId: "d1" }),
      ];

      const tree = buildDomainTree(items);
      // 环路被安全断开，形成有限树结构而非死循环
      expect(tree).toHaveLength(1);
      expect(tree[0].children).toHaveLength(1);
    });

    it("拍平树形结构并支持折叠子节点", () => {
      const items: Domain[] = [
        domain({ id: "d_root", name: "example.com" }),
        domain({ id: "d_sub1", name: "demo-server1.example.com" }),
        domain({ id: "d_sub2", name: "demo-web.example.com" }),
      ];

      const tree = buildDomainTree(items);
      const expandedRows = flattenDomainTree(tree, new Set());
      expect(expandedRows.map((r) => r.domain.name)).toEqual([
        "example.com",
        "demo-server1.example.com",
        "demo-web.example.com",
      ]);

      const collapsedRows = flattenDomainTree(tree, new Set(["d_root"]));
      expect(collapsedRows.map((r) => r.domain.name)).toEqual(["example.com"]);
    });

    it("拆分子域名前缀与父级后缀", () => {
      expect(splitDomainPrefix("demo.example.com", "example.com")).toEqual({
        prefix: "demo",
        parentSuffix: ".example.com",
      });
      expect(splitDomainPrefix("example.com", undefined)).toEqual({
        prefix: "example.com",
        parentSuffix: "",
      });
    });
  });
});
