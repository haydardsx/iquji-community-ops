import { z } from "zod";
import type { ApplicationDeploymentConfig } from "@/contracts/types";
import { sourceFormSchema, sourceFormValues, sourcePayload } from "./application-source";
import {
  commandArguments,
  commandDraft,
  commandDraftSchema,
  commandValue,
} from "./command-arguments";
import { registryFormValues, registryPayload, registrySyncForm } from "./registry-sync";
import {
  canonicalVolumeTarget,
  outputSchema,
  portSchema,
  validVolumeTarget,
  volumeNamePattern,
} from "./schemas";

const argumentList = commandDraftSchema.refine((draft) => {
  const { argv } = commandValue(draft);
  return !argv.length || commandArguments.safeParse(argv).success;
}, "请填写有效的首项和参数，或清除命令");

function localizedVariableKey(t?: (key: string) => string) {
  return z
    .string()
    .trim()
    .refine((key) => outputSchema.shape.key.safeParse(key).success, {
      message: t?.("projects.valVarKeyPattern") ?? "使用字母、数字和下划线，且不能以数字开头",
    });
}

export function createApplicationFormSchema(t?: (key: string, params?: any) => string) {
  const service = z.object({
    slotKey: z
      .string()
      .trim()
      .min(1, t ? t("projects.valServiceSlotRequired") : "请填写服务标识"),
    serviceName: z
      .string()
      .trim()
      .min(1, t ? t("projects.valServiceNameRequired") : "请填写 Swarm 服务名称"),
    serverId: z.string(),
    replicas: z
      .number()
      .int()
      .min(1, t ? t("projects.valReplicasMin") : "副本数至少为 1"),
    publishedPort: z.number().int().min(0).max(65535).optional(),
  });
  const variable = z.object({
    id: z.string().optional(),
    key: localizedVariableKey(t),
    valueSource: z.enum(["plain_text", "output_ref", "secret_ref"]),
    scope: z.enum(["global", "service"]),
    plainValue: z.string(),
    stage: z.enum(["runtime", "build"]),
    outputApplicationId: z.string().optional(),
    serviceSlotKey: z.string().optional(),
    metadata: z
      .object({
        valueType: z.string().optional(),
        isRequired: z.boolean().optional(),
        description: z.string().optional(),
      })
      .optional(),
  });
  return z
    .object({
      source: sourceFormSchema,
      registry: registrySyncForm,
      ports: z.array(portSchema),
      volumes: z.array(
        z.object({
          name: z
            .string()
            .refine(
              (name) => name === name.trim() && volumeNamePattern.test(name),
              t ? t("projects.valVolumeName") : "持久卷名称格式无效。",
            ),
          target: z
            .string()
            .refine(
              validVolumeTarget,
              t ? t("projects.valVolumeTarget") : "容器挂载目录格式无效。",
            ),
        }),
      ),
      command: argumentList,
      healthCommand: argumentList,
      logCommand: argumentList,
      initializeCommand: argumentList,
      buildServerId: z.string(),
      deploymentManagerServerId: z.string(),
      gatewayServerId: z.string(),
      eligibleServerIds: z.array(z.string()),
      containerPort: z
        .number()
        .int()
        .min(1, t ? t("projects.valPortMin") : "端口至少为 1")
        .max(65535, t ? t("projects.valPortMax") : "端口不能大于 65535")
        .optional(),
      publishedPort: z.number().int().min(0).max(65535).optional(),
      overlayNetwork: z.string(),
      services: z.array(service),
      variables: z.array(variable),
      outputs: z.array(outputSchema.extend({ key: localizedVariableKey(t) })),
    })
    .superRefine((value, ctx) => {
      if (value.source.provider === "git" && value.registry.mode === "enabled") {
        if (!value.registry.repository)
          ctx.addIssue({
            code: "custom",
            path: ["registry", "repository"],
            message: t ? t("projects.registryRepository") : "请填写镜像仓库",
          });
        if (Boolean(value.registry.username) !== Boolean(value.registry.secretRef))
          ctx.addIssue({
            code: "custom",
            path: ["registry", "secretRef"],
            message: t ? t("projects.registryCredentialPair") : "请同时填写仓库用户名和凭据",
          });
      }
      const slots = new Set<string>();
      value.services.forEach((item, index) => {
        if (slots.has(item.slotKey))
          ctx.addIssue({
            code: "custom",
            path: ["services", index, "slotKey"],
            message: t ? t("projects.valServiceSlotDuplicate") : "服务标识不能重复",
          });
        slots.add(item.slotKey);
      });
      const volumeNames = new Set<string>();
      const volumeTargets = new Set<string>();
      value.volumes.forEach((item, index) => {
        if (volumeNames.has(item.name))
          ctx.addIssue({
            code: "custom",
            path: ["volumes", index, "name"],
            message: t ? t("projects.valVolumeNameDuplicate") : "持久卷名称不能重复",
          });
        volumeNames.add(item.name);
        const target = canonicalVolumeTarget(item.target);
        if (volumeTargets.has(target))
          ctx.addIssue({
            code: "custom",
            path: ["volumes", index, "target"],
            message: t ? t("projects.valVolumeTargetDuplicate") : "持久卷挂载路径不能重复",
          });
        volumeTargets.add(target);
      });
      const keys = new Set<string>();
      value.variables.forEach((item, index) => {
        const key = item.key.trim();
        if (!key) {
          ctx.addIssue({
            code: "custom",
            path: ["variables", index, "key"],
            message: t ? t("projects.valVarKeyRequired") : "请填写变量名称",
          });
        } else if (
          keys.has(`${key}:${item.stage ?? "runtime"}:${item.scope}:${item.serviceSlotKey ?? ""}`)
        ) {
          ctx.addIssue({
            code: "custom",
            path: ["variables", index, "key"],
            message: t ? t("projects.valVarKeyDuplicate") : "同一作用范围的变量名称不能重复",
          });
        }
        keys.add(`${key}:${item.stage ?? "runtime"}:${item.scope}:${item.serviceSlotKey ?? ""}`);
        if (
          item.valueSource !== "plain_text" &&
          (!item.plainValue.trim() ||
            (item.valueSource === "output_ref" && !item.outputApplicationId))
        ) {
          ctx.addIssue({
            code: "custom",
            path: ["variables", index, "plainValue"],
            message:
              item.valueSource === "output_ref"
                ? (t?.("apps.varOutputRequired") ?? "请选择应用输出")
                : (t?.("projects.valSecretRefRequired") ?? "请选择应用凭据"),
          });
        }
        if (item.scope === "service" && !slots.has(item.serviceSlotKey ?? "")) {
          ctx.addIssue({
            code: "custom",
            path: ["variables", index, "serviceSlotKey"],
            message: t ? t("projects.valConfigureServicesFirst") : "请选择运行服务",
          });
        }
      });
    });
}

export const applicationFormSchema = createApplicationFormSchema();
export type ApplicationForm = z.infer<typeof applicationFormSchema>;

export function groupEnvironmentVariables(
  items: NonNullable<ApplicationDeploymentConfig["environmentVariables"]> = [],
): ApplicationForm["variables"] {
  return items.map((item) => ({
    id: item.id,
    key: item.key,
    stage: item.stage ?? "runtime",
    scope: item.scope ?? "global",
    outputApplicationId: item.outputApplicationId,
    serviceSlotKey: item.serviceSlotKey,
    valueSource: item.valueSource ?? "plain_text",
    plainValue:
      (item.valueSource === "secret_ref"
        ? item.secretRef
        : item.valueSource === "output_ref"
          ? item.outputId
          : item.plainValue) ?? "",
    metadata: {
      valueType: item.valueType,
      isRequired: item.isRequired,
      description: item.description,
    },
  }));
}

export function flattenEnvironmentVariables(
  variables: ApplicationForm["variables"],
  _services: ApplicationForm["services"],
) {
  return variables.map((item) => ({
    id: item.id,
    key: item.key.trim(),
    stage: item.stage,
    scope: item.scope,
    serviceSlotKey: item.scope === "service" ? item.serviceSlotKey : undefined,
    ...item.metadata,
    valueSource: item.valueSource,
    ...(item.valueSource === "secret_ref"
      ? { secretRef: item.plainValue }
      : item.valueSource === "output_ref"
        ? { outputId: item.plainValue, outputApplicationId: item.outputApplicationId }
        : { plainValue: item.plainValue }),
  }));
}

export function formValues(config: ApplicationDeploymentConfig): ApplicationForm {
  const source = config.sourceConfig;
  return {
    source: sourceFormValues(source),
    registry: registryFormValues(config.deliveryConfig?.registrySync),
    ports: config.swarmRuntime?.ports ?? [],
    volumes: config.swarmRuntime?.volumes ?? [],
    command: commandDraft(config.swarmRuntime?.command, config.swarmRuntime?.shellEntrypoint),
    healthCommand: commandDraft(config.swarmRuntime?.healthCommand),
    logCommand: commandDraft(config.swarmRuntime?.logCommand),
    initializeCommand: commandDraft(config.swarmRuntime?.initializeCommand),
    buildServerId: config.buildServerId ?? "",
    deploymentManagerServerId: config.deploymentManagerServerId ?? "",
    gatewayServerId: config.gatewayServerId ?? "",
    eligibleServerIds: config.placement?.eligibleServerIds ?? [],
    containerPort:
      config.swarmRuntime?.containerPort ??
      (config.swarmRuntime?.ports && config.swarmRuntime?.publishedPort === undefined
        ? undefined
        : 80),
    publishedPort: config.swarmRuntime?.publishedPort,
    overlayNetwork: config.swarmRuntime?.overlayNetwork ?? "",
    services: (config.swarmRuntime?.services ?? []).map((item) => ({
      slotKey: item.slotKey,
      serviceName: item.serviceName,
      serverId: item.serverId ?? "",
      replicas: item.replicas ?? 1,
      publishedPort: item.publishedPort,
    })),
    variables: groupEnvironmentVariables(config.environmentVariables),
    outputs: config.outputVariables ?? [],
  };
}
export function configPayload(
  value: ApplicationForm,
  baseline: ApplicationDeploymentConfig = {},
): ApplicationDeploymentConfig {
  const { argv: startup, shellEntrypoint } = commandValue(value.command);
  const health = commandValue(value.healthCommand).argv;
  const logQuery = commandValue(value.logCommand).argv;
  const initialize = commandValue(value.initializeCommand).argv;
  const { stackName: _removedStackName, ...runtime } = (baseline.swarmRuntime ?? {}) as NonNullable<
    ApplicationDeploymentConfig["swarmRuntime"]
  > & { stackName?: unknown };
  return {
    sourceConfig: sourcePayload(value.source),
    ...(value.source.provider === "git" && {
      deliveryConfig: {
        ...baseline.deliveryConfig,
        registrySync: registryPayload(value.registry),
      },
    }),
    buildServerId: value.source.provider === "git" ? value.buildServerId : "",
    deploymentManagerServerId: value.deploymentManagerServerId,
    gatewayServerId: value.gatewayServerId,
    placement: { eligibleServerIds: value.eligibleServerIds },
    swarmRuntime: {
      ...runtime,
      ports: value.ports,
      volumes: value.volumes,
      command: startup.length ? startup : undefined,
      shellEntrypoint,
      healthCommand: health.length ? health : undefined,
      logCommand: logQuery.length ? logQuery : undefined,
      initializeCommand: initialize.length ? initialize : undefined,
      containerPort: value.ports.length ? undefined : value.containerPort,
      publishedPort: value.ports.length ? undefined : value.publishedPort,
      overlayNetwork: value.overlayNetwork.trim(),
      services: value.services.map((item) => ({ ...item, serverId: item.serverId || undefined })),
    },
    environmentVariables: flattenEnvironmentVariables(value.variables, value.services),
    outputVariables: value.outputs,
  };
}
export function stateLabel(status?: string, t?: (key: any) => string) {
  if (t && status) {
    const key = `state.${status}`;
    const translated = t(key);
    if (translated && translated !== key) return translated;
  }
  return (
    (
      {
        running: "运行中",
        ready: "已就绪",
        starting: "启动中",
        preparing: "准备中",
        assigned: "已分配",
        pending: "等待中",
        shutdown: "已关闭",
        rejected: "已拒绝",
        complete: "已完成",
        stopped: "已停止",
        degraded: "部分异常",
        not_deployed: "未部署",
        unknown: "无法读取",
        idle: "待配置",
        succeeded: "已完成",
        failed: "失败",
        cancelled: "已取消",
        queued: "排队中",
        pending_confirmation: "待确认",
        executing: "执行中",
        verifying: "检查中",
      } as Record<string, string>
    )[status ?? ""] ??
    (status || "待配置")
  );
}
export function stateTone(status?: string): "neutral" | "success" | "warning" | "danger" | "info" {
  if (["running", "succeeded", "healthy", "active"].includes(status ?? "")) return "success";
  if (["failed", "degraded", "unhealthy"].includes(status ?? "")) return "danger";
  if (["queued", "pending_confirmation"].includes(status ?? "")) return "warning";
  return "neutral";
}
