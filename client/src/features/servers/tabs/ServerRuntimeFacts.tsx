import { IqujiBadge, IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { SurfacePanel } from "@/shared/ui/surface-panel";
import {
  runtimeComponentPresentation,
  swarmControlPresentation,
  swarmMembership,
  swarmMembershipDescription,
} from "../runtime-presentation";

import { swarmIdentityVerified } from "../swarm-connection";

export function ServerRuntimeFacts({
  metrics,
  server,
}: {
  metrics: ServerRuntimeMetrics;
  server: Server;
}) {
  const { t } = useI18n();
  const profile = (server.platformProfile ?? {}) as Record<string, unknown>;
  const [copied, setCopied] = useState(false);

  const osId = typeof profile.osId === "string" ? profile.osId : "";
  const osVersion = typeof profile.osVersion === "string" ? profile.osVersion : "";
  const architecture = typeof profile.architecture === "string" ? profile.architecture : "";
  const packageManager = typeof profile.packageManager === "string" ? profile.packageManager : "";
  const serviceManager = typeof profile.serviceManager === "string" ? profile.serviceManager : "";
  const mirrorUrl =
    typeof profile.dockerRegistryMirrorUrl === "string" && profile.dockerRegistryMirrorUrl
      ? profile.dockerRegistryMirrorUrl
      : (metrics.dockerRegistryMirrors?.[0] ?? "");

  const osText = [osId, osVersion].filter(Boolean).join(" ");
  const packageMgr = [packageManager, serviceManager].filter(Boolean).join(" / ");

  const dockerVersion = typeof metrics.dockerVersion === "string" ? metrics.dockerVersion : "";
  const membership = swarmMembership(metrics);
  const docker = runtimeComponentPresentation(metrics, "docker", t);
  const traefikService = typeof metrics.traefikService === "string" ? metrics.traefikService : "";
  const traefikHttpPort = metrics.traefikHttpPort ? String(metrics.traefikHttpPort) : "";
  const traefikHttpsPort = metrics.traefikHttpsPort ? String(metrics.traefikHttpsPort) : "";
  const traefikPorts = [traefikHttpPort, traefikHttpsPort].filter(Boolean).join(" / ");
  const jenkins = runtimeComponentPresentation(metrics, "jenkins", t);
  const traefik = runtimeComponentPresentation(metrics, "traefik", t);
  const agentVersion = typeof metrics.agentVersion === "string" ? metrics.agentVersion : "";

  const handleCopyMirror = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // 忽略剪贴板写入异常
    }
  };

  const role = swarmIdentityVerified(metrics) || membership === "inactive" ? membership : "unknown";
  const roleTone = role === "manager" ? "success" : role === "worker" ? "info" : "neutral";
  const roleLabel =
    role === "manager"
      ? t("swarm.manager")
      : role === "worker"
        ? t("swarm.worker")
        : role === "inactive"
          ? t("swarm.statusNotEnabled")
          : t("swarm.statusUnverified");
  const control = swarmControlPresentation(metrics, t);
  const agent = runtimeComponentPresentation(metrics, "agent", t);
  const mirrorTone: "success" | "warning" | "neutral" =
    metrics.dockerRegistryMirrorReady === true
      ? "success"
      : metrics.dockerRegistryMirrorReady === false
        ? "warning"
        : "neutral";
  const mirrorLabel =
    metrics.dockerRegistryMirrorReady === true
      ? t("servers.mirrorEffective")
      : metrics.dockerRegistryMirrorReady === false
        ? t("servers.mirrorNotEffective")
        : t("servers.notProbed");

  return (
    <SurfacePanel title={t("servers.specsTitle")}>
      <div className="ops-spec-section">
        <div className="ops-spec-compact-grid">
          {/* 操作系统 */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="server" size={13} />
              {t("servers.os")}
            </span>
            <span className="ops-spec-compact-item__val">
              {osText || t("servers.notProbed")}
              {architecture ? <IqujiBadge tone="neutral">{architecture}</IqujiBadge> : null}
            </span>
          </div>

          {/* 集群调度控制权 */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="shield" size={13} />
              {t("servers.clusterControl")}
            </span>
            <span className="ops-spec-compact-item__val">
              <IqujiBadge tone={control.tone}>{control.label}</IqujiBadge>
            </span>
          </div>

          {/* 包与服务管理 */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="terminal" size={13} />
              {t("servers.packageManager")}
            </span>
            <span className="ops-spec-compact-item__val">
              {packageMgr || t("servers.notProbed")}
            </span>
          </div>

          {/* Traefik 边缘网关 */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="network" size={13} />
              {t("servers.traefikService")}
            </span>
            <span className="ops-spec-compact-item__val">
              {metrics.ssh !== false && metrics.traefik === true && traefikService ? (
                <>
                  <span>{traefikService}</span>
                  {traefikPorts ? (
                    <span className="ops-spec-compact-item__muted">({traefikPorts})</span>
                  ) : null}
                </>
              ) : (
                <IqujiBadge tone={traefik.tone}>{traefik.label}</IqujiBadge>
              )}
            </span>
          </div>

          {/* Docker 引擎 */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="container" size={13} />
              {t("servers.dockerEngine")}
            </span>
            <span className="ops-spec-compact-item__val">
              {metrics.docker === true && metrics.ssh !== false && dockerVersion ? (
                <span>{dockerVersion}</span>
              ) : null}
              <IqujiBadge tone={docker.tone}>{docker.label}</IqujiBadge>
            </span>
          </div>

          {/* Jenkins CI */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="wrench" size={13} />
              {t("servers.jenkinsRunner")}
            </span>
            <span className="ops-spec-compact-item__val">
              <IqujiBadge tone={jenkins.tone}>{jenkins.label}</IqujiBadge>
            </span>
          </div>

          {/* Swarm 集群角色 */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="boxes" size={13} />
              {t("swarm.colRole")}
            </span>
            <span className="ops-spec-compact-item__val">
              <IqujiBadge tone={roleTone}>{roleLabel}</IqujiBadge>
              {swarmIdentityVerified(metrics) ? (
                <span>{swarmMembershipDescription(metrics, t)}</span>
              ) : null}
            </span>
          </div>

          {/* Ops Agent 状态 */}
          <div className="ops-spec-compact-item">
            <span className="ops-spec-compact-item__key">
              <IqujiIcon decorative name="activity" size={13} />
              {t("servers.agentStatus")}
            </span>
            <span className="ops-spec-compact-item__val">
              {metrics.agent === true && metrics.ssh !== false && agentVersion ? (
                <span>v{agentVersion}</span>
              ) : null}
              <IqujiBadge tone={agent.tone}>{agent.label}</IqujiBadge>
            </span>
          </div>
        </div>

        {/* 镜像加速与网络单行通告 */}
        {mirrorUrl ? (
          <div className="ops-spec-banner">
            <div className="ops-spec-banner__info">
              <IqujiIcon decorative name="globe" size={14} />
              <span className="ops-spec-banner__label">{t("servers.dockerMirror")}</span>
              <code className="ops-spec-banner__code" title={mirrorUrl}>
                {mirrorUrl}
              </code>
              <IqujiButton compact onClick={() => handleCopyMirror(mirrorUrl)} variant="ghost">
                <IqujiIcon decorative name={copied ? "check" : "copy"} size={12} />
                {copied ? t("common.copied") : t("common.copy")}
              </IqujiButton>
            </div>
            <IqujiBadge tone={mirrorTone}>{mirrorLabel}</IqujiBadge>
          </div>
        ) : null}
      </div>
    </SurfacePanel>
  );
}
