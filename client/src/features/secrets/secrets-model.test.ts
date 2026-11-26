import { describe, expect, it } from "vitest";
import type { SecretAsset, Server } from "@/contracts/types";
import {
  assetTypeLabel,
  bindableSshAssets,
  countByTab,
  derivePurpose,
  describeDeleteImpact,
  filterByQuery,
  filterByTab,
  linkedServers,
  materialLabel,
  purposeLabel,
} from "./secrets-model";

const asset = (patch: Partial<SecretAsset>): SecretAsset => ({
  id: "asset_1",
  assetType: "ssh_key",
  name: "部署密钥",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  ...patch,
});

const servers: Server[] = [
  {
    id: "srv_1",
    name: "gateway",
    publicHost: "203.0.113.1",
    secretAssetIds: ["asset_1"],
    createdAt: "",
    updatedAt: "",
  },
  { id: "srv_2", name: "worker", publicHost: "203.0.113.2", createdAt: "", updatedAt: "" },
];

describe("凭据模型", () => {
  it("未执行的新凭据不能进入绑定候选，缺失的旧材料仍可展示以便解绑", () => {
    expect(
      bindableSshAssets([
        asset({ id: "pending", materialStatus: "pending" }),
        asset({ id: "available", materialStatus: "available" }),
        asset({ id: "missing", materialStatus: "missing" }),
      ]).map((item) => item.id),
    ).toEqual(["available", "missing"]);
  });
  it("由类型派生用途与标签", () => {
    expect(derivePurpose("git_token")).toBe("git_auth");
    expect(derivePurpose("ssh_key")).toBe("deployment_server");
    expect(assetTypeLabel("git_token")).toBe("Git 凭据");
    expect(assetTypeLabel("ssh_key")).toBe("SSH 私钥");
    expect(assetTypeLabel("application_secret")).toBe("应用凭据");
    expect(purposeLabel("git_token")).toBe("代码仓库拉取认证");
    expect(purposeLabel("ssh_key", "deployment_server")).toBe("服务器登录与部署");
    expect(materialLabel("available")).toBe("材料就绪");
    expect(materialLabel(undefined)).toBe("材料缺失");
  });

  it("按服务器绑定关系反查关联主机", () => {
    expect(linkedServers("asset_1", servers)).toEqual(["gateway"]);
    expect(linkedServers("asset_missing", servers)).toEqual([]);
  });

  it("按类型与关键字过滤", () => {
    const items = [
      asset({ id: "asset_1", assetType: "ssh_key", name: "部署密钥" }),
      asset({ id: "asset_2", assetType: "git_token", name: "GitHub", description: "私有仓库" }),
    ];
    expect(filterByTab(items, "git").map((item) => item.id)).toEqual(["asset_2"]);
    expect(filterByTab(items, "ssh").map((item) => item.id)).toEqual(["asset_1"]);
    expect(filterByTab(items, "all")).toHaveLength(2);
    expect(filterByQuery(items, "私有", servers).map((item) => item.id)).toEqual(["asset_2"]);
    expect(filterByQuery(items, "gateway", servers).map((item) => item.id)).toEqual(["asset_1"]);
    expect(filterByQuery(items, "  ", servers)).toHaveLength(2);
    expect(countByTab(items)).toEqual({ all: 2, git: 1, ssh: 1, application: 0, registry: 0 });
  });

  it("只把 SSH 私钥列为可绑定凭据", () => {
    const items = [asset({ id: "a1" }), asset({ id: "a2", assetType: "git_token" })];
    expect(bindableSshAssets(items).map((item) => item.id)).toEqual(["a1"]);
  });

  it("删除文案区分级联、阻断与无引用", () => {
    const empty = describeDeleteImpact(
      {
        usage: { servers: [], gitApplications: [], applicationVariables: [] },
        blocked: false,
      },
      "部署密钥",
    );
    expect(empty).toContain("没有服务器、Git 认证或环境配置引用该凭据");

    const cascade = describeDeleteImpact(
      {
        usage: {
          servers: ["gateway"],
          gitApplications: ["API"],
          applicationVariables: [],
        },
        blocked: false,
      },
      "部署密钥",
    );
    expect(cascade).toContain("将从 1 台服务器解绑：gateway");
    expect(cascade).toContain("将清除 1 个应用的 Git 认证：API");

    const blocked = describeDeleteImpact(
      {
        usage: {
          servers: [],
          gitApplications: [],
          applicationVariables: ["DB_PASSWORD"],
        },
        blocked: true,
      },
      "部署密钥",
    );
    expect(blocked).toContain("仍被运行配置引用");
    expect(blocked).toContain("DB_PASSWORD");
  });
});
