import { describe, expect, it } from "vitest";
import { flattenEnvironmentVariables, groupEnvironmentVariables } from "./model";

describe("应用变量保存", () => {
  it("密钥引用往返保存后不会变成明文", () => {
    const input = [
      {
        key: "PGPASSWORD",
        stage: "runtime" as const,
        scope: "global" as const,
        valueSource: "secret_ref" as const,
        secretRef: "secret_pg",
      },
    ];
    expect(flattenEnvironmentVariables(groupEnvironmentVariables(input), [])).toEqual(input);
  });

  it("全局值和单个服务覆盖分别保留，不为其他服务创建覆盖", () => {
    const input = [
      {
        key: "DEMO_ENV",
        stage: "runtime" as const,
        scope: "global" as const,
        valueSource: "plain_text" as const,
        plainValue: "global",
      },
      {
        key: "DEMO_ENV",
        stage: "runtime" as const,
        scope: "service" as const,
        serviceSlotKey: "backend-a",
        valueSource: "plain_text" as const,
        plainValue: "override",
      },
    ];
    const services = ["backend-a", "backend-b"].map((slotKey) => ({
      slotKey,
      serviceName: slotKey,
      serverId: "srv",
      replicas: 1,
    }));
    expect(flattenEnvironmentVariables(groupEnvironmentVariables(input), services)).toEqual(input);
  });

  it("保存已有配置时保留用户选择的来源", () => {
    expect(
      groupEnvironmentVariables([
        { key: "PGHOST", scope: "global", valueSource: "plain_text", plainValue: "db" },
      ])[0].valueSource,
    ).toBe("plain_text");
  });

  it("跨应用构建引用保留稳定的输出标识", () => {
    const input = [
      {
        key: "APP_URL",
        scope: "global" as const,
        stage: "build" as const,
        valueSource: "output_ref" as const,
        outputId: "out_address",
        outputApplicationId: "app_service",
      },
    ];
    expect(flattenEnvironmentVariables(groupEnvironmentVariables(input), [])).toEqual(input);
  });
});
