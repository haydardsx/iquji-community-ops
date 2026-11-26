import {
  createRootRoute,
  createRoute,
  createRouter,
  lazyRouteComponent,
  redirect,
  type SearchSchemaInput,
} from "@tanstack/react-router";
import { workspacePages } from "@/contracts/pages";

import { ShellLayout } from "./shell";

const rootRoute = createRootRoute({
  component: ShellLayout,
  validateSearch: (search: Record<string, unknown>): { operationId?: string } => ({
    operationId: typeof search.operationId === "string" ? search.operationId : undefined,
  }),
});

export type MonitoringRouteSearch = {
  tab?: "projects" | "servers" | "swarm";
};

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: workspacePages.home.path,
  validateSearch: (search: Record<string, unknown>): MonitoringRouteSearch => ({
    tab: (["projects", "servers", "swarm"] as const).find((t) => t === search.tab) ?? "projects",
  }),
  component: lazyRouteComponent(() => import("@/pages/monitoring"), "MonitoringPage"),
});

const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: workspacePages.projects.path,
  validateSearch: (
    input: SearchSchemaInput & {
      projectId?: unknown;
      applicationId?: unknown;
      projectView?: unknown;
      projectTab?: unknown;
      applicationTab?: unknown;
      search?: unknown;
      appSearch?: unknown;
    },
  ) => ({
    projectId: typeof input.projectId === "string" ? input.projectId : undefined,
    applicationId: typeof input.applicationId === "string" ? input.applicationId : undefined,
    projectView: input.projectView === "overview" ? ("overview" as const) : undefined,
    projectTab:
      input.projectTab === "operations" ? ("operations" as const) : ("applications" as const),
    applicationTab:
      (["source", "runtime", "container", "routes", "variables", "logs"] as const).find(
        (value) => value === input.applicationTab,
      ) ?? ("source" as const),
    search: typeof input.search === "string" ? input.search : "",
    appSearch: typeof input.appSearch === "string" ? input.appSearch : "",
  }),
  component: lazyRouteComponent(() => import("@/pages/projects"), "ProjectsPage"),
});

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/dashboard",
  beforeLoad: () => {
    throw redirect({ to: workspacePages.home.path });
  },
});

export type ServersRouteSearch = {
  serverId?: string;
};

const serversRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: workspacePages.servers.path,
  validateSearch: (search: Record<string, unknown>): ServersRouteSearch => ({
    serverId: typeof search.serverId === "string" ? search.serverId : undefined,
  }),
  component: lazyRouteComponent(() => import("@/pages/servers"), "ServersPage"),
});

const swarmRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/swarm",
  beforeLoad: () => {
    throw redirect({ to: workspacePages.home.path, search: { tab: "swarm" } });
  },
});

const tunnelsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: workspacePages.tunnels.path,
  validateSearch: (search: Record<string, unknown>): { tunnelId?: string } => ({
    tunnelId: typeof search.tunnelId === "string" ? search.tunnelId : undefined,
  }),
  component: lazyRouteComponent(() => import("@/pages/tunnels"), "TunnelsPage"),
});

const secretsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/secrets",
  beforeLoad: () => {
    throw redirect({ to: workspacePages.settings.path, hash: "secrets" });
  },
});

const domainCertificatesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/domain-certificates",
  beforeLoad: () => {
    throw redirect({ to: workspacePages.settings.path, hash: "domain-certificates" });
  },
});

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: workspacePages.settings.path,
  component: lazyRouteComponent(() => import("@/pages/settings"), "SettingsPage"),
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  dashboardRoute,
  projectsRoute,
  serversRoute,
  swarmRoute,
  tunnelsRoute,
  secretsRoute,
  domainCertificatesRoute,
  settingsRoute,
]);

export const router = createRouter({
  routeTree,
  defaultPreload: "intent",
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
