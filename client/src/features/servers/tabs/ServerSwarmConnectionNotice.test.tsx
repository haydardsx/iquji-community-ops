import { describe, expect, it } from "vitest";
import type { Server } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { privateIPv4Boundary } from "@/test/swarm-addresses";
import { ServerSwarmConnectionNotice } from "./ServerSwarmConnectionNotice";

const privateEndpoint = `${privateIPv4Boundary}:3377`;

function manager(metrics: Record<string, unknown>): Server {
  return {
    id: "server_manager",
    name: "示例管理节点",
    publicHost: "192.0.2.20",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    health: { metrics: { swarm: true, swarmVerificationStatus: "verified", ...metrics } },
  };
}

describe("Swarm 节点互通说明", () => {
  it("显示实际完整控制端点并保留非默认端口", () => {
    const markup = renderToStaticMarkup(
      <ServerSwarmConnectionNotice
        managers={[
          manager({ swarmManagerAddress: "192.0.2.20:3377", swarmAdvertiseAddr: "192.0.2.20" }),
        ]}
      />,
    );
    expect(markup).toContain("manager 控制端点 192.0.2.20:3377");
    expect(markup).toContain("TCP/UDP 7946");
    expect(markup).toContain("仅限集群节点访问");
  });

  it("旧快照缺少可信通告事实时显示待核验", () => {
    const markup = renderToStaticMarkup(
      <ServerSwarmConnectionNotice managers={[manager({ swarmAdvertiseAddr: "192.0.2.20" })]} />,
    );
    expect(markup).toContain("待核验");
    expect(markup).not.toContain("192.0.2.20:2377");
  });

  it("选择公网加入时明确指出目标 manager 仍通告私网", () => {
    const markup = renderToStaticMarkup(
      <ServerSwarmConnectionNotice
        managers={[manager({ swarmManagerAddress: privateEndpoint })]}
        connectionMode="public"
      />,
    );
    expect(markup).toContain(`实际 manager 控制端点 ${privateEndpoint}`);
    expect(markup).toContain("目标 manager 当前使用私网");
    expect(markup).toContain("请先重建目标 manager 的通信网络");
    expect(markup).toContain("备份并重建 Swarm");
    expect(markup).toContain("重建完成并核验实际地址后再加入");
  });

  it("当 showMismatchAlert 为 false 时不展示通信模式不匹配警告", () => {
    const markup = renderToStaticMarkup(
      <ServerSwarmConnectionNotice
        managers={[manager({ swarmManagerAddress: privateEndpoint })]}
        connectionMode="public"
        showMismatchAlert={false}
      />,
    );
    expect(markup).toContain(`实际 manager 控制端点 ${privateEndpoint}`);
    expect(markup).not.toContain("请先重建目标 manager 的通信网络");
  });
});
