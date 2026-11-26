import { zhCNAutomation } from "./automation";
import { zhCNBackup } from "./backup";
import { zhCNCommon } from "./common";
import { zhCNMonitoring } from "./monitoring";
import { zhCNOperations } from "./operations";
import { zhCNProjects } from "./projects";
import { zhCNSecrets } from "./secrets";
import { zhCNServers } from "./servers";
import { zhCNSettings } from "./settings";
import { zhCNSwarm } from "./swarm";
import { zhCNTunnels } from "./tunnels";
import { zhCNTutorial } from "./tutorial";

export const zhCN = {
  ...zhCNAutomation,
  ...zhCNBackup,
  ...zhCNCommon,
  ...zhCNProjects,
  ...zhCNServers,
  ...zhCNSecrets,
  ...zhCNSwarm,
  ...zhCNTunnels,
  ...zhCNTutorial,
  ...zhCNMonitoring,
  ...zhCNOperations,
  ...zhCNSettings,
} as const;

export type OpsMessageKey = keyof typeof zhCN;
export type OpsMessageCatalog = Record<OpsMessageKey, string>;
