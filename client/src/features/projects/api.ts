import { z } from "zod";
import type {
  Application,
  ApplicationDeploymentConfig,
  ApplicationRuntime,
  ApplicationServiceLogs,
  DeploymentEligibility,
  OperationRun,
  Page,
  Project,
  ProjectWorkspace,
  RouteInput,
  RouteRow,
} from "@/contracts/types";
import { fetchJson } from "@/shared/api/client";
import { deploymentSchema, operationSchema, routeSchema } from "./schemas";

const availability = z.enum(["available", "not_deployed", "unavailable"]);
const runtimeSchema = z.object({
  availability,
  unavailableReason: z.string().optional(),
  status: z.enum(["running", "stopped", "degraded", "not_deployed", "unknown"]),
  checkedAt: z.string(),
  services: z.array(
    z.object({
      serviceSlotKey: z.string(),
      serviceId: z.string(),
      serviceName: z.string(),
      serverId: z.string(),
      expectedReplicas: z.number(),
      desiredReplicas: z.number(),
      runningReplicas: z.number(),
      ports: z
        .array(
          z.object({
            name: z.string(),
            protocol: z.enum(["http", "tcp", "udp"]),
            containerPort: z.number().int().min(1).max(65535),
            publishedPort: z.number().int().min(1).max(65535).optional(),
            publishedHost: z.string().optional(),
            internalVerified: z.boolean().optional(),
            publishedVerified: z.boolean().optional(),
          }),
        )
        .optional(),
      tasks: z.array(
        z.object({
          id: z.string(),
          nodeId: z.string(),
          serverId: z.string().optional(),
          status: z.string(),
          error: z.string().optional(),
        }),
      ),
    }),
  ),
});
const logsSchema = z.object({
  availability,
  unavailableReason: z.string().optional(),
  checkedAt: z.string(),
  truncated: z.boolean(),
  items: z.array(
    z.object({
      id: z.string(),
      message: z.string(),
      timestamp: z.string().optional(),
      source: z.enum(["swarm", "application"]),
      serviceSlotKey: z.string(),
    }),
  ),
});
export const fetchProjects = () => fetchJson<Page<Project>>("projects");
export const fetchProjectApplications = (projectId: string) =>
  fetchJson<Page<Application>>(`projects/${projectId}/applications`);
export const fetchWorkspace = (id: string) =>
  fetchJson<ProjectWorkspace>(`projects/${id}/workspace`);
export const createProject = (input: { name: string; description: string }) =>
  fetchJson<Project>("projects", { method: "POST", body: input });
export const updateProject = (id: string, input: { name: string; description: string }) =>
  fetchJson<Project>(`projects/${id}`, { method: "PUT", body: input });
export const deleteProject = (id: string) =>
  fetchJson<{ ok: boolean }>(`projects/${id}`, { method: "DELETE" });
const templatesSchema = z.object({
  version: z.literal(1),
  items: z.array(
    z.object({
      id: z.string(),
      name: z.string(),
      description: z.string(),
      config: z.record(z.string(), z.unknown()),
    }),
  ),
});
export type TemplateCatalog = z.infer<typeof templatesSchema>;
export type ApplicationTemplate = TemplateCatalog["items"][number];
export const fetchApplicationTemplates = async () =>
  templatesSchema.parse(await fetchJson("projects/application-templates"));
export const createApplication = (
  projectId: string,
  input: { name: string; templateId?: string },
) => fetchJson<Application>(`projects/${projectId}/applications`, { method: "POST", body: input });
export const updateApplication = (id: string, input: { name: string }) =>
  fetchJson<Application>(`projects/applications/${id}`, { method: "PUT", body: input });
export const deleteApplication = (id: string) =>
  fetchJson<{ ok: boolean }>(`projects/applications/${id}`, { method: "DELETE" });
export const saveDeploymentConfig = async (
  id: string,
  input: ApplicationDeploymentConfig,
): Promise<ApplicationDeploymentConfig> =>
  deploymentSchema.parse(
    await fetchJson(`projects/applications/${id}/deployment-config`, {
      method: "PUT",
      body: input,
    }),
  );
export async function fetchApplicationRuntime(id: string): Promise<ApplicationRuntime> {
  return runtimeSchema.parse(await fetchJson(`projects/applications/${id}/runtime`));
}
export async function fetchServiceLogs(id: string, slot: string): Promise<ApplicationServiceLogs> {
  return logsSchema.parse(
    await fetchJson(
      `logs/runtime?targetKind=application&targetId=${encodeURIComponent(id)}&source=service&serviceSlotKey=${encodeURIComponent(slot)}`,
    ),
  );
}
export async function fetchApplicationCommandLogs(
  id: string,
  slot: string,
  taskId: string,
  signal?: AbortSignal,
) {
  const params = new URLSearchParams({
    targetKind: "application",
    targetId: id,
    source: "application",
    serviceSlotKey: slot,
    taskId,
  });
  return logsSchema.parse(await fetchJson(`logs/runtime?${params}`, { method: "POST", signal }));
}

export const runApplicationAction = async (
  id: string,
  action: "start" | "stop" | "restart" | "uninstall",
  requestKey?: string,
): Promise<OperationRun> =>
  operationSchema.parse(
    await fetchJson(`projects/applications/${id}/${action}`, {
      method: "POST",
      ...(requestKey ? { requestKey } : {}),
      body: {},
    }),
  );
export const createRoute = async (id: string, input: RouteInput): Promise<RouteRow> =>
  routeSchema.parse(
    await fetchJson(`projects/applications/${id}/routes`, { method: "POST", body: input }),
  );
export const updateRoute = async (
  id: string,
  routeId: string,
  input: RouteInput,
): Promise<RouteRow> =>
  routeSchema.parse(
    await fetchJson(`projects/applications/${id}/routes/${routeId}`, {
      method: "PUT",
      body: input,
    }),
  );
export const deleteRoute = async (
  id: string,
  routeId: string,
  requestKey?: string,
): Promise<OperationRun> =>
  operationSchema.parse(
    await fetchJson(`projects/applications/${id}/routes/${routeId}`, {
      method: "DELETE",
      requestKey,
    }),
  );
export const publishRoute = async (
  id: string,
  routeId: string,
  requestKey?: string,
): Promise<OperationRun> =>
  operationSchema.parse(
    await fetchJson(`projects/applications/${id}/routes/${routeId}/publish`, {
      method: "POST",
      ...(requestKey ? { requestKey } : {}),
      body: {},
    }),
  );
export const disableRoute = async (
  id: string,
  routeId: string,
  requestKey?: string,
): Promise<OperationRun> =>
  operationSchema.parse(
    await fetchJson(`projects/applications/${id}/routes/${routeId}/status`, {
      method: "PATCH",
      ...(requestKey ? { requestKey } : {}),
      body: { disabled: true },
    }),
  );

export function fetchDeploymentEligibility(
  applicationId: string,
  deploymentManagerServerId: string,
) {
  return fetchJson<DeploymentEligibility>(
    `projects/applications/${encodeURIComponent(applicationId)}/deployment-eligibility?deploymentManagerServerId=${encodeURIComponent(deploymentManagerServerId)}`,
  );
}
