import { z } from "zod";
import { sourceSchema } from "./application-source";
export const operationSchema = z.object({
  id: z.string(),
  action: z.string(),
  status: z.string(),
  targetKind: z.string(),
  targetId: z.string(),
  targetName: z.string().optional(),
  summary: z.string().optional(),
  errorCode: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export const outputSchema = z.object({
  id: z.string().min(1),
  key: z.string().regex(/^[A-Za-z_][A-Za-z0-9_]*$/),
  visibility: z.enum(["project", "cross_project"]),
  valueSource: z.enum([
    "plain_text",
    "variable",
    "secret_ref",
    "service_host",
    "service_port",
    "route_url",
    "static_url",
    "build_output",
  ]),
  plainValue: z.string().optional(),
  secretRef: z.string().optional(),
  variableKey: z.string().optional(),
  variableStage: z.enum(["runtime", "build"]).optional(),
  serviceSlotKey: z.string().optional(),
  portName: z.string().optional(),
  endpointMode: z.enum(["internal", "published"]).optional(),
  routeId: z.string().optional(),
});
export const portSchema = z.object({
  name: z.string().regex(/^[a-z][a-z0-9-]{0,39}$/),
  protocol: z.enum(["http", "tcp", "udp"]),
  targetPort: z.number().int().min(1).max(65535),
  publishedPort: z.number().int().min(0).max(65535).optional(),
});
export const volumeNamePattern = /^[a-z][a-z0-9-]{0,39}$/;
export function canonicalVolumeTarget(target: string) {
  return `/${target.split("/").filter(Boolean).join("/")}`;
}
export function nextVolumeDefaults(volumes: readonly { name: string; target: string }[]) {
  const names = new Set(volumes.map((volume) => volume.name));
  const targets = new Set(volumes.map((volume) => canonicalVolumeTarget(volume.target)));
  let index = 1;
  while (true) {
    const suffix = index === 1 ? "" : `-${index}`;
    const name = `data${suffix}`;
    const target = `/data${suffix}`;
    if (!names.has(name) && !targets.has(target)) return { name, target };
    index += 1;
  }
}
export function validVolumeTarget(target: string) {
  if (
    !target.startsWith("/") ||
    target.startsWith("//") ||
    target.endsWith("/") ||
    target.includes("//") ||
    /[\0\r\n,]/.test(target)
  )
    return false;
  const segments = target.slice(1).split("/");
  return Boolean(
    segments.length && segments.every((part) => part && part !== "." && part !== ".."),
  );
}
const variableSchema = z.object({
  id: z.string().optional(),
  key: z.string(),
  valueSource: z.enum(["plain_text", "secret_ref", "output_ref"]).optional(),
  stage: z.enum(["runtime", "build"]).optional(),
  outputId: z.string().optional(),
  outputApplicationId: z.string().optional(),
  plainValue: z.string().optional(),
  secretRef: z.string().optional(),
  maskedSummary: z.string().optional(),
  valueType: z.string().optional(),
  isRequired: z.boolean().optional(),
  description: z.string().optional(),
  scope: z.enum(["global", "service"]).optional(),
  serviceSlotKey: z.string().optional(),
});
const serviceSchema = z.object({
  slotKey: z.string(),
  serverId: z.string().optional(),
  serviceName: z.string(),
  replicas: z.number().optional(),
  publishedPort: z.number().optional(),
});
export const deploymentSchema = z
  .object({
    id: z.string().optional(),
    applicationId: z.string().optional(),
    sourceConfig: sourceSchema.optional(),
    buildServerId: z.string().optional(),
    deploymentManagerServerId: z.string().optional(),
    gatewayServerId: z.string().optional(),
    placement: z.object({ eligibleServerIds: z.array(z.string()).optional() }).optional(),
    environmentVariables: z.array(variableSchema).optional(),
    outputVariables: z.array(outputSchema).optional(),
    deliveryConfig: z.record(z.unknown()).optional(),
    swarmRuntime: z
      .object({
        ports: z.array(portSchema).optional(),
        volumes: z.array(z.object({ name: z.string(), target: z.string() })).optional(),
        command: z.array(z.string()).optional(),
        initializeCommand: z.array(z.string()).optional(),
        healthCommand: z.array(z.string()).optional(),
        logCommand: z.array(z.string()).optional(),
        containerPort: z.number().optional(),
        publishedPort: z.number().optional(),
        overlayNetwork: z.string().optional(),
        services: z.array(serviceSchema).optional(),
      })
      .passthrough()
      .optional(),
    createdAt: z.string().optional(),
    updatedAt: z.string().optional(),
  })
  .passthrough();
const domainSchema = z.object({
  id: z.string(),
  name: z.string(),
  parentDomainId: z.string().optional(),
  certificateId: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export const routeSchema = z.object({
  id: z.string(),
  applicationId: z.string(),
  projectId: z.string().optional(),
  domainIds: z.array(z.string()).optional(),
  domains: z.array(domainSchema),
  services: z.array(
    z.object({
      slotKey: z.string(),
      serviceName: z.string(),
      weight: z.number().int().min(0).max(2147483647),
      portName: z.string().optional(),
    }),
  ),
  rules: z.array(
    z.object({
      path: z.string(),
      pathHandling: z.enum(["preserve", "strip_prefix", "rewrite"]),
      rewriteTargetPath: z.string().optional(),
    }),
  ),
  disabled: z.boolean(),
  status: z.string().optional(),
  accessibleUrls: z.array(z.string()).optional(),
  lastPublishedAt: z.string().optional(),
  lastPublishSummary: z.string().optional(),
  lastApplied: z.record(z.unknown()).optional(),
  pendingRemote: z.record(z.unknown()).optional(),
  createdAt: z.string().optional(),
  updatedAt: z.string().optional(),
});
