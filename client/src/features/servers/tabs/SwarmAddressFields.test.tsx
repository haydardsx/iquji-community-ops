import { describe, expect, it } from "vitest";
import type { Server } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { privateIPv4Boundary, privateIPv6Boundary } from "@/test/swarm-addresses";
import { SwarmAddressFields, swarmAddressError } from "./SwarmAddressFields";

const server: Server = {
  id: "server_example",
  name: "示例节点",
  publicHost: "192.0.2.10",
  privateHost: privateIPv4Boundary,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

describe("Swarm 通信网络表单", () => {
  it("公网与私网地址必须符合显式选择，IPv6 和云映射地址可使用", () => {
    expect(swarmAddressError("public", "192.0.2.10")).toBeUndefined();
    expect(swarmAddressError("public", "2001:db8::10")).toBeUndefined();
    expect(swarmAddressError("private", privateIPv4Boundary)).toBeUndefined();
    expect(swarmAddressError("private", privateIPv6Boundary)).toBeUndefined();
    expect(swarmAddressError("public", privateIPv4Boundary)).toContain("属于私网");
    expect(swarmAddressError("private", "192.0.2.10")).toContain("属于公网");
    expect(swarmAddressError("public", "example.test")).toContain("IPv4 或 IPv6");
    expect(swarmAddressError("public", "0.0.0.0")).toBeDefined();
    expect(swarmAddressError("private", "127.0.0.1")).toBeDefined();
  });

  it("表单提供网络选择和本节点地址，所选网络不冒充已探测事实", () => {
    const markup = renderToStaticMarkup(
      <SwarmAddressFields
        server={server}
        connectionMode="public"
        advertiseAddr="192.0.2.10"
        onConnectionModeChange={() => {}}
        onAdvertiseAddrChange={() => {}}
      />,
    );
    expect(markup).toContain("集群通信网络");
    expect(markup).toContain("本节点通告 IP");
    expect(markup).toContain('value="192.0.2.10"');
    expect(markup).toContain("云公网映射地址可直接填写");
    expect(markup).toContain('aria-label="本节点通告 IP的说明"');
    expect(markup).not.toContain("实际连接");
  });

  it("通告地址保留必填标签、说明和当前错误的读屏关联", () => {
    const markup = renderToStaticMarkup(
      <SwarmAddressFields
        server={server}
        connectionMode="public"
        advertiseAddr={privateIPv4Boundary}
        onConnectionModeChange={() => {}}
        onAdvertiseAddrChange={() => {}}
      />,
    );
    const input = markup.match(/<input[^>]*aria-invalid="true"[^>]*>/)?.[0] ?? "";
    const id = input.match(/ id="([^"]+)"/)?.[1];
    expect(id).toBeTruthy();
    expect(input).toContain("required");
    expect(input).toContain(`aria-describedby="${id}-description ${id}-error"`);
    expect(markup).toContain(`for="${id}"`);
    expect(markup).toContain(`id="${id}-error" role="alert"`);
  });
});
