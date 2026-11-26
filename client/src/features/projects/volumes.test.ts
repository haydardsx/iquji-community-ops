import { describe, expect, it } from "vitest";
import { applicationFormSchema, configPayload, formValues } from "./model";
import { deploymentSchema, nextVolumeDefaults } from "./schemas";

describe("应用持久卷配置", () => {
  it("保留旧挂载路径，修改指定行后即可重新保存", () => {
    const logs = { name: "logs", target: "/logs" };
    const config = deploymentSchema.parse({
      sourceConfig: { provider: "image", image: "example/service:1" },
      swarmRuntime: { volumes: [logs, { name: "data", target: "/data/" }] },
    });
    const form = formValues(config);
    expect(form.volumes[1].target).toBe("/data/");
    const invalid = applicationFormSchema.safeParse(form);
    expect(invalid.error?.issues.map((issue) => issue.path)).toEqual([["volumes", 1, "target"]]);
    form.volumes[1] = { ...form.volumes[1], target: "/data" };
    const saved = configPayload(applicationFormSchema.parse(form), config);
    expect(saved.swarmRuntime?.volumes).toEqual([logs, { name: "data", target: "/data" }]);
  });

  it("生成不重复的卷名和挂载路径", () => {
    expect(nextVolumeDefaults([])).toEqual({ name: "data", target: "/data" });
    expect(
      nextVolumeDefaults([
        { name: "data", target: "/data" },
        { name: "data-2", target: "/custom" },
      ]),
    ).toEqual({ name: "data-3", target: "/data-3" });
    expect(nextVolumeDefaults([{ name: "logs", target: "/data" }])).toEqual({
      name: "data-2",
      target: "/data-2",
    });
  });

  it("校验卷名、挂载路径及各自的重复项", () => {
    const form = formValues({});
    form.volumes = [
      { name: "data", target: "/data" },
      { name: "data", target: "/logs" },
    ];
    let result = applicationFormSchema.safeParse(form);
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues).toContainEqual(
        expect.objectContaining({ path: ["volumes", 1, "name"] }),
      );
    }

    form.volumes = [
      { name: "data", target: "/data" },
      { name: "logs", target: "/data" },
    ];
    result = applicationFormSchema.safeParse(form);
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues).toContainEqual(
        expect.objectContaining({ path: ["volumes", 1, "target"] }),
      );
    }

    form.volumes = [
      { name: "Data", target: "/data//logs" },
      { name: "data-2", target: "/data/../logs" },
    ];
    result = applicationFormSchema.safeParse(form);
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues.map((issue) => issue.path)).toEqual(
        expect.arrayContaining([
          ["volumes", 0, "name"],
          ["volumes", 0, "target"],
          ["volumes", 1, "target"],
        ]),
      );
    }
  });
});
