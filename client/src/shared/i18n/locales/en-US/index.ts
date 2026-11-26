import type { OpsMessageCatalog } from "../zh-CN";
import { enUSAutomation } from "./automation";
import { enUSBackup } from "./backup";
import { enUSCommon } from "./common";
import { enUSMonitoring } from "./monitoring";
import { enUSOperations } from "./operations";
import { enUSProjects } from "./projects";
import { enUSSecrets } from "./secrets";
import { enUSServers } from "./servers";
import { enUSSettings } from "./settings";
import { enUSSwarm } from "./swarm";
import { enUSTunnels } from "./tunnels";
import { enUSTutorial } from "./tutorial";

export const enUS: OpsMessageCatalog = {
  ...enUSAutomation,
  ...enUSBackup,
  ...enUSCommon,
  ...enUSProjects,
  ...enUSServers,
  ...enUSSecrets,
  ...enUSSwarm,
  ...enUSTunnels,
  ...enUSTutorial,
  ...enUSMonitoring,
  ...enUSOperations,
  ...enUSSettings,
};
