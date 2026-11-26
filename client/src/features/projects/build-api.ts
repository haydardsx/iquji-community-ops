import { z } from "zod";
import { fetchJson, fetchText } from "@/shared/api/client";
import { operationSchema } from "./schemas";

const buildSchema = z.object({
  id: z.string(),
  applicationId: z.string(),
  operationRunId: z.string(),
  status: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
  provider: z.string().optional(),
  pullMirrors: z
    .array(
      z.object({
        serverId: z.string(),
        role: z.enum(["build", "deploy", "gateway", "buildGateway"]),
        origin: z.enum(["application", "server", "daemon"]),
        addresses: z.array(z.string()),
        probed: z.boolean(),
      }),
    )
    .optional(),
  artifacts: z
    .array(
      z.object({
        serviceSlotKey: z.string(),
        baseImageDigest: z.string().optional(),
        configDigest: z.string().optional(),
        manifestDigest: z.string().optional(),
        serverId: z.string().optional(),
        registryImage: z.string().optional(),
        registryDigest: z.string().optional(),
        image: z.string(),
        imageDigest: z.string(),
        platform: z.string().optional(),
        packageName: z.string().optional(),
        packageVersion: z.string().optional(),
      }),
    )
    .optional(),
  summary: z.string().optional(),
  errorCode: z.string().optional(),
  commitSha: z.string().optional(),
  imageDigest: z.string().optional(),
  runUrl: z.string().optional(),
  stageUrl: z.string().optional(),
  consoleUrl: z.string().optional(),
});
export type ApplicationBuild = z.infer<typeof buildSchema>;
export async function fetchApplicationBuilds(applicationId: string) {
  return z
    .object({ items: z.array(buildSchema) })
    .parse(await fetchJson(`logs/builds?applicationId=${encodeURIComponent(applicationId)}`));
}
export async function requestApplicationBuild(applicationId: string, requestKey?: string) {
  return operationSchema.parse(
    await fetchJson(`projects/applications/${applicationId}/rebuild`, {
      method: "POST",
      ...(requestKey ? { requestKey } : {}),
      body: {},
    }),
  );
}
export async function cancelApplicationBuild(operationId: string) {
  return operationSchema.parse(
    await fetchJson(`operations/runs/${operationId}/cancel`, { method: "POST", body: {} }),
  );
}

export type BuildArtifactKind = "summary" | "stage" | "console";

export async function fetchBuildArtifact(url: string, kind: BuildArtifactKind) {
  const safeURL = safeBuildURL(url);
  if (!safeURL?.startsWith("/api/")) {
    throw new Error("构建详情地址不是受管 OPS 日志地址。");
  }
  const path = safeURL.slice("/api/".length);
  return kind === "console" ? fetchText(path) : fetchJson<Record<string, unknown>>(path);
}

export function safeBuildURL(value?: string) {
  if (!value) return undefined;
  if (/^\/api\/logs\/builds\/[a-zA-Z0-9_-]+\/(console|stage|summary)$/.test(value)) return value;
  try {
    const parsed = new URL(value);
    return ["https:", "http:"].includes(parsed.protocol) && !parsed.username && !parsed.password
      ? parsed.href
      : undefined;
  } catch {
    return undefined;
  }
}
