import { z } from "zod";

export function createRouteServicesSchema(t?: (key: string) => string) {
  return z
    .array(
      z.object({
        slotKey: z.string().min(1),
        portName: z.string().optional(),
        weight: z
          .number()
          .int(t ? t("projects.routeWeightInteger") : "权重须为整数")
          .min(0, t ? t("projects.routeWeightNonNegative") : "权重不能小于 0")
          .max(2147483647),
      }),
    )
    .min(1, t ? t("projects.routeServiceRequired") : "至少选择一个运行服务")
    .superRefine((services, ctx) => {
      if (new Set(services.map((item) => item.slotKey)).size !== services.length)
        ctx.addIssue({
          code: "custom",
          message: t ? t("projects.routeServiceDuplicate") : "运行服务不能重复",
        });
      if (!services.some((item) => item.weight > 0))
        ctx.addIssue({
          code: "custom",
          path: [0, "weight"],
          message: t ? t("projects.routeWeightPositive") : "至少一个目标服务的权重必须大于 0",
        });
    });
}

export const routeServicesSchema = createRouteServicesSchema();
