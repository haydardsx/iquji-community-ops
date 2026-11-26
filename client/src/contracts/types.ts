export type Id = string;
export type Timestamp = string;

export type Project = {
  builtin?: boolean;
  id: Id;
  name: string;
  description?: string;
  tags?: string[];
  applicationIds?: Id[];
  serverIds?: Id[];
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type Application = {
  inputChanges?: Array<{
    key: string;
    stage: "build" | "runtime";
    source: string;
    serviceSlotKey?: string;
    applicationName?: string;
    outputName?: string;
    credentialName?: string;
  }>;
  inputStatus?: "restart" | "rebuild" | "unavailable" | "";
  id: Id;
  projectId: Id;
  name: string;
  status?: string;
  runtimeMode?: string;
  sourceKind?: string;
  deploymentEngine?: string;
  upgradeStrategy?: string;
  runtimeSnapshot?: ApplicationRuntime;
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type SwarmDeparture = {
  nodeId: string;
  clusterId: string;
  managerServerId: string;
  managerAddress: string;
  cleanupStatus: "leaving" | "pending" | "removed" | "leave_failed";
  reasonCode: string;
  departedAt: string;
  updatedAt: string;
  operationRunId: string;
};

export type Server = {
  connectionProxy?: { mode: "direct" | "manual"; url?: string; hasCredentials?: boolean };
  sshHostFingerprint?: string;
  id: Id;
  name: string;
  publicHost: string;
  privateHost?: string;
  port?: number;
  sshUser?: string;
  environment?: string;
  status?: string;
  labels?: string[];
  projectIds?: Id[];
  secretAssetIds?: Id[];
  platformProfile?: Record<string, unknown>;
  createdAt: Timestamp;
  updatedAt: Timestamp;
  health?: Record<string, unknown>;
  swarmDepartures?: SwarmDeparture[];
};

export type OperationRun = {
  id: Id;
  action: string;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled" | string;
  targetKind: string;
  targetId: Id;
  targetName?: string;
  summary?: string;
  errorCode?: string;
  queue?: {
    state: string;
    position: number;
    canCancel: boolean;
    reason?: string;
    blockedByOperationRunId?: string;
  };
  details?: {
    swarmRebuildRecovery?: { needsRecovery: boolean; operationRunId: Id };
    [key: string]: unknown;
  };
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type OperationRunPage = {
  items: OperationRun[];
  page: number;
  pageSize: number;
  total: number;
};

export type DashboardSummary = {
  metrics: { projectCount: number; applicationCount: number; deploymentsLast7Days: number };
  statusCounts: { running: number; errored: number; idle: number };
  recentDeployments: Array<{
    id: Id;
    projectId?: Id;
    applicationId: Id;
    applicationName: string;
    versionLabel: string;
    status: string;
    createdAt: Timestamp;
  }>;
  recentProgress?: unknown[];
};

export type Page<T> = { items: T[]; page: number; pageSize: number; total: number };

export type SecretAsset = {
  id: Id;
  assetType: string;
  purpose?: string;
  name: string;
  secretRef?: string;
  fingerprint?: string;
  maskedSummary?: string;
  materialStatus?: string;
  description?: string;
  serverId?: Id;
  rotatedAt?: string;
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type Domain = {
  id: Id;
  name: string;
  parentDomainId?: Id;
  certificateId?: Id;
  redirect?: DomainRedirect;
  redirectRuntime?: { status?: "pending" | "active" | "failed"; lastErrorCode?: string };
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type DomainRedirect = {
  gatewayServerId: Id;
  targetDomainId: Id;
  scheme: "http" | "https";
  mode: "permanent" | "temporary";
};

export type Certificate = {
  id: Id;
  name: string;
  issuer?: string;
  challengeType?: string;
  domains?: string[];
  resolverId?: Id;
  lastVerifiedAt?: Timestamp;
  expiresAt?: Timestamp;
  status?: string;
  renewalStatus?: string;
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type Resolver = {
  id: Id;
  name: string;
  ingressId: Id;
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type Ingress = {
  id: Id;
  name: string;
  serverIds: Id[];
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type FrpTunnel = {
  id: Id;
  name: string;
  serverId: Id;
  frpsBindPort: number;
  mappings: Array<Record<string, unknown>>;
  status?: string;
  runCount?: number;
  probeHitCount?: number;
  lastRunAt?: string;
  createdAt: Timestamp;
  updatedAt: Timestamp;
};

export type LocalPlatformConfig = {
  controlPlane: {
    hostname: string;
    apiHost: string;
    apiPort: number;
    publicBaseUrl?: string;
    dataDirectory: string;
    version: string;
  };
  generatedAt: Timestamp;
};

export type RouteRow = {
  id: Id;
  applicationId: Id;
  projectId?: Id;
  status?: string;
  domainIds?: Id[];
  domains: Domain[];
  services: RouteInput["services"];
  rules: RouteInput["rules"];
  disabled: boolean;
  accessibleUrls?: string[];
  lastPublishedAt?: Timestamp;
  lastPublishSummary?: string;
  lastApplied?: Record<string, unknown>;
  pendingRemote?: Record<string, unknown>;
  createdAt?: Timestamp;
  updatedAt?: Timestamp;
};

export type RouteInput = {
  domainIds: Id[];
  services: Array<{ slotKey: string; serviceName: string; weight: number; portName?: string }>;
  rules: Array<{
    path: string;
    pathHandling: "preserve" | "strip_prefix" | "rewrite";
    rewriteTargetPath?: string;
  }>;
  disabled: boolean;
};

export type ApplicationSourceConfig =
  | {
      provider: "git";
      repositoryUrl: string;
      ref: string;
      refType: "branch" | "tag" | "commit";
      jenkinsfilePath: string;
      sourcePath: string;
      registryMirrorUrl?: string;
      authSecretRef?: string;
    }
  | { provider: "image"; image: string; registryMirrorUrl?: string }
  | {
      provider: "apt" | "dnf" | "yum" | "npm" | "pypi";
      baseImage: string;
      packageName: string;
      version: string;
      command: string[];
      shellEntrypoint?: true;
    };

export type ApplicationEnvironmentVariable = {
  id?: Id;
  key: string;
  valueSource?: "plain_text" | "secret_ref" | "output_ref";
  stage?: "runtime" | "build";
  outputId?: string;
  outputApplicationId?: string;
  plainValue?: string;
  secretRef?: string;
  maskedSummary?: string;
  valueType?: string;
  isRequired?: boolean;
  description?: string;
  scope?: "global" | "service";
  serviceSlotKey?: string;
};

export type ApplicationOutput = {
  id: string;
  key: string;
  visibility: "project" | "cross_project";
  valueSource:
    | "plain_text"
    | "variable"
    | "secret_ref"
    | "service_host"
    | "service_port"
    | "route_url"
    | "static_url"
    | "build_output";
  plainValue?: string;
  secretRef?: string;
  variableKey?: string;
  variableStage?: "build" | "runtime";
  serviceSlotKey?: string;
  portName?: string;
  endpointMode?: "internal" | "published";
  routeId?: string;
};
export type AvailableOutput = {
  sensitive: boolean;
  id: string;
  key: string;
  applicationId: string;
  applicationName: string;
  projectId: string;
};
export type ContainerPort = {
  name: string;
  protocol: "http" | "tcp" | "udp";
  targetPort: number;
  publishedPort?: number;
};
export type ContainerVolume = { name: string; target: string };
export type SwarmServiceConfig = {
  slotKey: string;
  serverId?: Id;
  serviceName: string;
  replicas?: number;
  publishedPort?: number;
};

export type ApplicationDeploymentConfig = {
  id?: Id;
  applicationId?: Id;
  sourceConfig?: ApplicationSourceConfig;
  buildServerId?: Id;
  deploymentManagerServerId?: Id;
  gatewayServerId?: Id;
  placement?: { eligibleServerIds?: Id[] };
  environmentVariables?: ApplicationEnvironmentVariable[];
  outputVariables?: ApplicationOutput[];
  deliveryConfig?: Record<string, unknown>;
  swarmRuntime?: {
    ports?: ContainerPort[];
    volumes?: ContainerVolume[];
    command?: string[];
    shellEntrypoint?: true;
    healthCommand?: string[];
    logCommand?: string[];
    initializeCommand?: string[];
    containerPort?: number;
    publishedPort?: number;
    overlayNetwork?: string;
    services?: SwarmServiceConfig[];
  };
  createdAt?: Timestamp;
  updatedAt?: Timestamp;
};

export type ProjectWorkspace = {
  availableOutputs?: AvailableOutput[];
  project: Project | null;
  applications: Application[];
  selectedApplication?: Application | null;
  applicationInstances?: Array<Record<string, unknown>>;
  applicationDeploymentConfigs?: ApplicationDeploymentConfig[];
  routes: RouteRow[];
  relatedOperations?: OperationRun[];
  servers?: Server[];
  relatedServers?: Server[];
  domains?: Domain[];
  certificates?: Certificate[];
  resolvers?: Resolver[];
  ingresses?: Ingress[];
};

export type ApplicationRuntime = {
  availability: "available" | "not_deployed" | "unavailable";
  unavailableReason?: string;
  status: "running" | "stopped" | "degraded" | "not_deployed" | "unknown";
  checkedAt: Timestamp;
  services: Array<{
    serviceSlotKey: string;
    serviceId: string;
    serviceName: string;
    serverId: Id;
    expectedReplicas: number;
    desiredReplicas: number;
    runningReplicas: number;
    ports?: Array<{
      name: string;
      protocol: "http" | "tcp" | "udp";
      containerPort: number;
      publishedPort?: number;
      publishedHost?: string;
      internalVerified?: boolean;
      publishedVerified?: boolean;
    }>;
    tasks: Array<{ id: string; nodeId: string; serverId?: string; status: string; error?: string }>;
  }>;
};

export type ApplicationServiceLogs = {
  availability: ApplicationRuntime["availability"];
  unavailableReason?: string;
  checkedAt: Timestamp;
  truncated: boolean;
  items: Array<{
    id: string;
    message: string;
    timestamp?: Timestamp;
    source: "swarm" | "application";
    serviceSlotKey: string;
  }>;
};

export type ServerRuntimeMetrics = {
  ssh?: boolean;
  docker?: boolean;
  swarm?: boolean;
  swarmRole?: string;
  swarmControlAvailable?: boolean;
  swarmClusterId?: string;
  swarmNodeId?: string;
  swarmManagerServerId?: string;
  swarmAdvertiseAddr?: string;
  swarmManagerAddress?: string;
  swarmConnectionMode?: "public" | "private" | "unknown";
  swarmLocalNodeState?: string;
  swarmVerificationStatus?: "verified" | "stale" | "unverified" | "not_joined";
  swarmVerificationError?: string;
  swarmCheckedAt?: string;
  dockerVersion?: string;
  dockerRegistryMirrorReady?: boolean;
  dockerRegistryMirrors?: string[];
  checkedAt?: string;
  traefik?: boolean;
  frps?: boolean;
  frpsStatus?: "unknown" | "missing" | "unavailable" | "installed" | "running";
  frpsPresent?: boolean;
  frpsManagedPresent?: boolean;
  frpsExternal?: boolean;
  frpsInUse?: boolean;
  frpsIdle?: boolean;
  frpsImage?: string;
  frpsProbeError?: string;
  jenkins?: boolean;
  agent?: boolean;
  [key: string]: unknown;
};

export type DeploymentEligibility = {
  deploymentManagerServerId: Id;
  deploymentClusterId: string;
  servers: Array<{
    serverId: Id;
    eligible: boolean;
    clusterId: string;
    role: string;
    reasons: Array<{ code: string; message: string }>;
  }>;
};
