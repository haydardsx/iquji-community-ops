import { describe, expect, it } from "vitest";
import { routeServicesSchema } from "./route-services";
import { routeSchema } from "./schemas";

describe("访问入口权重", () => {
  it("同一服务在不同入口保留独立权重，零权重允许暂停流量", () => {
    for (const weights of [
      [1, 3],
      [0, 1],
      [3, 1],
    ]) {
      const services = weights.map((weight, index) => ({ slotKey: `service-${index}`, weight }));
      expect(routeServicesSchema.parse(services)).toEqual(services);
    }
  });

  it("阻止全零、负数、小数、无效数字、溢出与重复目标", () => {
    for (const weight of [0, -1, 0.5, Number.NaN, 2147483648])
      expect(routeServicesSchema.safeParse([{ slotKey: "web", weight }]).success).toBe(false);
    expect(
      routeServicesSchema.safeParse([
        { slotKey: "web", weight: 1 },
        { slotKey: "web", weight: 3 },
      ]).success,
    ).toBe(false);
  });

  it("API 解析保留入口各目标的权重", () => {
    const services = [
      { slotKey: "web", serviceName: "demo-web", weight: 1 },
      { slotKey: "api", serviceName: "demo-api", weight: 3 },
    ];
    expect(
      routeSchema.parse({
        id: "route-demo",
        applicationId: "app-demo",
        domains: [],
        services,
        rules: [],
        disabled: false,
      }).services,
    ).toEqual(services);
  });
});
