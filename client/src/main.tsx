import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "@iquji-community/web-theme/tokens.css";
import "@iquji-community/web-component/styles.css";
import "@iquji-community/web-editor/styles.css";
import "./shared/styles/theme-override.css";
import "./shared/styles/app.css";
import "./shared/styles/ops-form.css";
import "./shared/styles/ops-shell.css";
import "./shared/styles/ops-project.css";
import "./shared/styles/ops-project-variables.css";
import "./shared/styles/ops-scope.css";
import "./shared/styles/ops-settings.css";
import "./shared/styles/ops-server.css";
import "./shared/styles/ops-tunnel.css";
import "./shared/styles/ops-tunnel-probe.css";
import "./shared/styles/ops-monitoring.css";
import "./shared/styles/ops-swarm.css";

import { App } from "./app/App";

const root = document.getElementById("root");
if (!root) throw new Error("应用缺少根节点");

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
