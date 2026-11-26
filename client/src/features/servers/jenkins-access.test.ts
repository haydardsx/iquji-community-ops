import { describe, expect, it, vi } from "vitest";
import { fetchJson } from "@/shared/api/client";
import {
  bindJenkinsAccess,
  fetchJenkinsAccess,
  unbindJenkinsAccess,
  validViewerPassword,
  verifiedJenkinsURL,
} from "./jenkins-access";

vi.mock("@/shared/api/client", () => ({ fetchJson: vi.fn() }));
describe("Jenkins 访问入口", () => {
  it("只打开已生效且不含凭据的 HTTPS 地址", () => {
    const state = { status: "active" as const, ready: true };
    expect(verifiedJenkinsURL({ ...state, url: "https://jenkins.example.org" })).toBe(
      "https://jenkins.example.org/",
    );
    for (const url of [
      "http://jenkins.example.org",
      "https://user@jenkins.example.org",
      "https://jenkins.example.org/?password=x",
    ])
      expect(verifiedJenkinsURL({ ...state, url })).toBe("");
    expect(
      verifiedJenkinsURL({ ...state, status: "pending", url: "https://jenkins.example.org" }),
    ).toBe("");
  });
  it("按字符和 UTF-8 字节校验密码", () => {
    expect(validViewerPassword("示".repeat(12))).toBe(true);
    expect(validViewerPassword("示".repeat(43))).toBe(false);
    expect(validViewerPassword("x".repeat(11))).toBe(false);
    expect(validViewerPassword(`${"x".repeat(12)}\n`)).toBe(false);
  });
  it("绑定与撤销使用同一资源路径和幂等请求标识", async () => {
    const body = {
      domainId: "domain",
      gatewayServerId: "gateway",
      viewerPassword: "demo-password",
    };
    const path = "servers/server%2Fone/jenkins/access";
    await fetchJenkinsAccess("server/one");
    expect(fetchJson).toHaveBeenLastCalledWith(path);
    await bindJenkinsAccess("server/one", body, "request-one");
    expect(fetchJson).toHaveBeenLastCalledWith(path, {
      method: "POST",
      body,
      requestKey: "request-one",
    });
    await unbindJenkinsAccess("server/one", "request-two");
    expect(fetchJson).toHaveBeenLastCalledWith(path, {
      method: "DELETE",
      requestKey: "request-two",
    });
  });
});
