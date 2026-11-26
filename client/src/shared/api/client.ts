import { createHttpTransport } from "@iquji-community/web-network";
import { z } from "zod";
import { desktopSession } from "@/shared/desktop/window-session";
import { domainRedirectImpact } from "./domain-errors";
import { jenkinsInstallErrors, operationErrors } from "./operation-errors";

const transport = createHttpTransport({ timeoutMs: 180_000 });
const basePath = "/api";

export type ApiErrorBody = {
  errorCode?: string;
  message?: string;
  traceId?: string;
  details?: unknown;
  [key: string]: unknown;
};

export class ApiError extends Error {
  readonly status: number;
  readonly errorCode?: string;
  readonly traceId?: string;
  readonly body?: ApiErrorBody;

  constructor(input: { status: number; body?: ApiErrorBody; fallbackMessage: string }) {
    super(input.body?.message ?? input.body?.errorCode ?? input.fallbackMessage);
    this.name = "ApiError";
    this.status = input.status;
    this.errorCode = input.body?.errorCode;
    this.traceId = input.body?.traceId;
    this.body = input.body;
  }
}

export type RequestOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  form?: FormData;
  signal?: AbortSignal;
  requestKey?: string;
};
function url(path: string): string {
  return `${basePath}/${path.replace(/^\/+/, "")}`;
}
async function readError(response: Response): Promise<ApiErrorBody | undefined> {
  try {
    return (await response.clone().json()) as ApiErrorBody;
  } catch {
    return undefined;
  }
}
async function send(path: string, options: RequestOptions = {}): Promise<Response> {
  const headers = new Headers();
  const bearer = await desktopSession();
  if (bearer) headers.set("Authorization", `Bearer ${bearer}`);
  if (options.requestKey) headers.set("Idempotency-Key", options.requestKey);
  let body: BodyInit | undefined;
  if (options.form) {
    body = options.form;
  } else if (options.body !== undefined) {
    headers.set("content-type", "application/json");
    body = JSON.stringify(options.body);
  }
  const response = await transport(url(path), {
    method: options.method ?? "GET",
    headers,
    body,
    signal: options.signal,
  });
  if (!response.ok) {
    throw new ApiError({
      status: response.status,
      body: await readError(response),
      fallbackMessage: `请求失败（HTTP ${response.status}）`,
    });
  }
  return response;
}

export async function fetchJson<T>(path: string, options?: RequestOptions): Promise<T> {
  return (await (await send(path, options)).json()) as T;
}

export async function fetchText(path: string, options?: RequestOptions): Promise<string> {
  return (await (await send(path, options)).text()) as string;
}

export async function fetchDownload(
  path: string,
  options?: RequestOptions,
): Promise<{ blob: Blob; filename: string }> {
  const response = await send(path, options);
  const disposition = response.headers.get("content-disposition") ?? "";
  const encoded = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
  const filename = encoded
    ? decodeURIComponent(encoded)
    : (disposition.match(/filename="?([^";]+)"?/i)?.[1] ?? "iquops-backup.iquops-backup");
  return { blob: await response.blob(), filename };
}

export function errorMessage(error: unknown, t?: (key: any, params?: any) => string): string {
  if (error instanceof ApiError) {
    if (error.errorCode) return operationErrorMessage(error.errorCode, error.body?.details, t);
    return error.message;
  }
  if (error instanceof Error) {
    return operationErrorMessage(error.message, undefined, t);
  }
  return t ? t("common.opFailedRetry") : "操作失败，请重试。";
}

const resourceRefSchema = z
  .object({ kind: z.string().optional(), id: z.string().optional(), name: z.string().optional() })
  .passthrough();
const checkItemSchema = z
  .object({
    code: z.string().optional(),
    summary: z.string().optional(),
    target: resourceRefSchema.optional(),
    details: z.record(z.string(), z.unknown()).optional(),
  })
  .passthrough();
const previewDetailsSchema = z
  .object({ blockers: z.array(z.string()).optional(), checks: z.array(checkItemSchema).optional() })
  .passthrough();
const impactReasonSchema = z
  .object({
    code: z.string().optional(),
    resourceKind: z.string().optional(),
    resourceId: z.string().optional(),
    resourceName: z.string().optional(),
    applicationName: z.string().optional(),
    serverName: z.string().optional(),
    field: z.string().optional(),
  })
  .passthrough();
const impactDetailsSchema = z
  .object({ reasons: z.array(impactReasonSchema).optional() })
  .passthrough();

export function operationErrorMessage(
  value?: string,
  context?: unknown,
  t?: (key: any, params?: any) => string,
): string {
  if (!value) return t ? t("common.opFailedRetry") : "暂时无法获取原因，请刷新后重试。";
  const [code, ...suffix] = value.split(":");
  if (Object.hasOwn(jenkinsInstallErrors, code)) {
    return jenkinsInstallErrors[code];
  }
  const structured = structuredOperationDetails(code, context);
  if (structured.length > 0) return `${structured.join("；")}。`;
  const blockers = operationBlockerMessages(value);
  if (blockers.length > 0) {
    const title = code === "FRP_TUNNEL_INVALID" ? "隧道配置有误" : "启动前检查未通过";
    return `${title}，${blockers.join("；")}。`;
  }
  return operationErrors[code]
    ? operationErrors[code] + (suffix.length ? `（${suffix.join(":")}）` : "")
    : value;
}

function structuredOperationDetails(code: string, value: unknown): string[] {
  if (code === "DOMAIN_REDIRECT_TARGET_IN_USE") return domainRedirectImpact(value);
  if (code.startsWith("APPLICATION_OUTPUT_") || code === "APPLICATION_ROUTE_OUTPUT_IN_USE") {
    const result = z.object({ affectedApplications: z.array(resourceRefSchema) }).safeParse(value);
    if (result.success) {
      const names = result.data.affectedApplications
        .map((app) => (app.name && app.id ? `${app.name}（${app.id}）` : app.name || app.id))
        .filter(Boolean);
      if (names.length) return [`${operationErrors[code] ?? code} 受影响应用：${names.join("、")}`];
    }
  }
  if (code === "RELEASE_PLAN_PREVIEW_BLOCKED") {
    const parsed = previewDetailsSchema.safeParse(value);
    if (!parsed.success) return [];
    const checks = parsed.data.checks ?? [];
    const messages = checks.map(previewCheckMessage).filter(Boolean);
    if (messages.length > 0) return messages;
    return (parsed.data.blockers ?? []).map(operationBlockerMessage);
  }
  if (code.startsWith("SWARM_UNINSTALL_BLOCKED_")) {
    const impact = parseImpactDetails(value);
    if (!impact) return [];
    const messages = (impact.reasons ?? []).map(swarmImpactReasonMessage).filter(Boolean);
    if (messages.length > 0) return messages;
  }
  return [];
}

function parseImpactDetails(value: unknown): z.infer<typeof impactDetailsSchema> | null {
  const wrapped = z.object({ impact: impactDetailsSchema }).passthrough().safeParse(value);
  if (wrapped.success) return wrapped.data.impact;
  const direct = impactDetailsSchema.safeParse(value);
  return direct.success ? direct.data : null;
}

function previewCheckMessage(check: z.infer<typeof checkItemSchema>): string {
  const code = check.code ?? "";
  const target = check.target?.name || check.target?.id;
  const details = check.details ?? {};
  if (code === "SWARM_CLUSTER_MISMATCH") {
    const manager =
      typeof details.deploymentManagerServerName === "string"
        ? details.deploymentManagerServerName
        : "部署管理节点";
    return `服务器「${target || "部署服务器"}」与「${manager}」不在同一 Swarm 集群`;
  }
  const labels: Record<string, string> = {
    BUILD_SERVER_SSH_UNAVAILABLE: "构建服务器 SSH 不可用",
    BUILD_SERVER_DOCKER_UNAVAILABLE: "构建服务器 Docker 不可用",
    BUILD_SERVER_JENKINS_UNAVAILABLE: "构建服务器 Jenkins 不可用",
    BUILD_SERVER_NOT_AVAILABLE_TO_PROJECT: "构建服务器不属于当前项目",
    GATEWAY_SERVER_SSH_UNAVAILABLE: "网关服务器 SSH 不可用",
    GATEWAY_SERVER_DOCKER_UNAVAILABLE: "网关服务器 Docker 不可用",
    GATEWAY_SERVER_SWARM_MANAGER_REQUIRED: "网关服务器不是可用的 Swarm manager",
    GATEWAY_SERVER_TRAEFIK_UNAVAILABLE: "网关服务器 Traefik 不可用",
    GATEWAY_SWARM_MANAGER_REQUIRED: "网关服务器必须是可用的 Swarm manager",
    SWARM_SERVICE_REQUIRED: "至少需要一个 Swarm 服务",
    DEPLOYMENT_SERVER_SSH_UNAVAILABLE: "部署服务器 SSH 不可用",
    DEPLOYMENT_MANAGER_SSH_UNAVAILABLE: "部署管理节点 SSH 不可用",
    DEPLOYMENT_MANAGER_DOCKER_UNAVAILABLE: "部署管理节点 Docker 不可用",
    DEPLOYMENT_MANAGER_SWARM_MANAGER_REQUIRED: "部署管理节点 Swarm 未就绪",
    DEPLOYMENT_MANAGER_ROLE_REQUIRED: "请在运行服务中选择可用的 Swarm 管理节点。",
    DEPLOYMENT_SERVER_DOCKER_UNAVAILABLE: "部署服务器 Docker 不可用",
    DEPLOYMENT_SERVER_SWARM_UNAVAILABLE: "部署服务器 Swarm 不可用",
    DEPLOYMENT_SERVER_NOT_AVAILABLE_TO_PROJECT: "部署服务器不属于当前项目",
  };
  const message = labels[code] ?? operationErrors[code] ?? check.summary ?? code;
  return target && !message.includes(target) ? `服务器「${target}」：${message}` : message;
}

export function swarmImpactReasonMessage(reason: z.infer<typeof impactReasonSchema>): string {
  const code = reason.code ?? "";
  const labels = [
    reason.serverName ? `服务器「${reason.serverName}」` : "",
    reason.applicationName ? `应用「${reason.applicationName}」` : "",
    reason.resourceName && !reason.serverName && !reason.applicationName
      ? `资源「${reason.resourceName}」`
      : "",
  ].filter(Boolean);
  const subject = labels.length > 0 ? labels.join("、") : "目标服务器";
  const reasons: Record<string, string> = {
    SERVER_HAS_MANAGED_TRAEFIK_RUNTIME: `仍运行受管 Traefik，请先卸载网关服务`,
    SERVER_HAS_MANAGED_FRPS_RUNTIME: `仍安装受管 FRPS，请先卸载穿透服务`,
    SERVER_REFERENCED_AS_GATEWAY: `仍被应用配置用作网关`,
    SERVER_REFERENCED_BY_PLACEMENT: `仍在应用调度范围内`,
    SERVER_REFERENCED_BY_SWARM_SERVICE: `仍被应用 Swarm 服务引用`,
    SERVER_HAS_APPLICATION_INSTANCE: `仍有应用实例，请先卸载对应 Swarm 服务`,
    SERVER_REFERENCED_BY_ACTIVE_RELEASE_GATEWAY: `仍被活动发布计划用作网关`,
    SERVER_REFERENCED_BY_ACTIVE_RELEASE_SERVICE: `仍被活动发布计划引用`,
    SERVER_IS_SWARM_CLUSTER_MEMBER: `仍属于 Swarm 集群，请先处理集群成员`,
    SERVER_HAS_ACTIVE_TUNNEL_RUN: `仍有活动内网穿透，请先停止并清理`,
    SERVER_REFERENCED_BY_TUNNEL: `仍被内网穿透配置引用`,
    SERVER_HAS_ACTIVE_TASK: `仍有正在处理的服务器操作，请完成或取消后重试`,
    SWARM_UNFINISHED_JOIN_EXIT_BLOCKED: `保留着 Swarm 成员身份，请先重新探测并按成员角色退出`,
  };
  if (reasons[code]) return `${subject}${reasons[code]}`;
  return `${subject}仍有相关资源需要处理，请刷新服务器状态后重试`;
}

/** 拆分 `FRP_TUNNEL_PREVIEW_BLOCKED:CODE,CODE` 这类复合阻断码并逐条翻译。 */
export function operationBlockerMessages(value?: string): string[] {
  const [code, ...rest] = (value ?? "").split(":");
  if (
    !["FRP_TUNNEL_INVALID", "FRP_TUNNEL_PREVIEW_BLOCKED", "RELEASE_PLAN_PREVIEW_BLOCKED"].includes(
      code,
    ) ||
    rest.length === 0
  ) {
    return [];
  }
  return rest
    .join(":")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean)
    .map(operationBlockerMessage);
}

export function operationBlockerMessage(code: string): string {
  const [head, detail] = code.split(":");
  if (head === "LOCAL_TCP_PORT_UNREACHABLE") {
    return detail
      ? `映射「${detail}」的本地端口未监听，请先启动对应服务`
      : "本地端口未监听，请先启动对应服务";
  }
  if (!head) return code;
  // 错误码表里每条已经自带句号，拼成一句时去掉末尾标点避免「。；」。
  const message = operationErrors[head] ?? code;
  return message.replace(/。$/u, "");
}
