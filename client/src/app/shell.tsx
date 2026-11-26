import {
  IconButton,
  IqujiDesktopRail,
  type IqujiDesktopRailItem,
  IqujiDesktopShell,
  IqujiDesktopTitleBar,
  StatusBadge,
} from "@iquji-community/web-component";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { createContext, useContext, useEffect, useMemo, useState } from "react";
import { type PageID, workspacePageOrder, workspacePages } from "@/contracts/pages";
import { type OpsMessageKey, useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { ActionGuideOverlay } from "@/shared/tutorial/ActionGuideOverlay";
import { TutorialMenu, TutorialOverlay } from "@/shared/tutorial/Tutorial";
import { OperationLogHost } from "@/shared/ui/operation-log-drawer";
import { AutomationWindow } from "./AutomationWindow";

const pagePresentation = {
  home: { labelKey: "nav.home", icon: "home" },
  projects: { labelKey: "nav.projects", icon: "folder" },
  servers: { labelKey: "nav.servers", icon: "server" },
  tunnels: { labelKey: "nav.tunnels", icon: "network" },
  settings: { labelKey: "nav.settings", icon: "settings" },
} as const satisfies Record<PageID, { labelKey: OpsMessageKey; icon: IqujiIconName }>;

const COLLAPSE_KEY = "iquops.sidebarCollapsed";

type ShellContextValue = {
  sidebarCollapsed: boolean;
  setSidebarCollapsed: React.Dispatch<React.SetStateAction<boolean>>;
};

const ShellContext = createContext<ShellContextValue>({
  sidebarCollapsed: false,
  setSidebarCollapsed: () => {},
});

export const useShell = () => useContext(ShellContext);

export function ShellLayout() {
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const [sidebarCollapsed, setSidebarCollapsed] = useState(readCollapsed);
  const [tourExpanded, setTourExpanded] = useState(false);
  const { t } = useI18n();

  useEffect(() => {
    try {
      window.localStorage.setItem(COLLAPSE_KEY, String(sidebarCollapsed));
    } catch {
      // localStorage disabled ignore
    }
  }, [sidebarCollapsed]);

  const navItems = useMemo(() => {
    return workspacePageOrder.map((id) => ({
      id,
      label: t(pagePresentation[id].labelKey),
      icon: pagePresentation[id].icon,
      to: workspacePages[id].path,
    }));
  }, [t]);

  const currentNav = useMemo(() => {
    if (pathname === workspacePages.home.path) {
      return navItems[0];
    }
    return (
      navItems.find(
        (item) =>
          item.to !== workspacePages.home.path &&
          (pathname === item.to || pathname.startsWith(`${item.to}/`)),
      ) ?? navItems[0]
    );
  }, [pathname, navItems]);

  const hasMasterSidebar = useMemo(() => {
    return (
      pathname === workspacePages.projects.path ||
      pathname.startsWith(workspacePages.servers.path) ||
      pathname.startsWith(workspacePages.tunnels.path)
    );
  }, [pathname]);

  const railItems = useMemo<IqujiDesktopRailItem[]>(() => {
    return navItems.map((item) => {
      const active =
        item.to === workspacePages.home.path
          ? pathname === workspacePages.home.path
          : pathname === item.to || pathname.startsWith(`${item.to}/`);
      return {
        active,
        badge:
          item.id === "home" ? undefined : (
            <OperationActivityDot
              scope={{ navigation: item.id as "projects" | "servers" | "tunnels" | "settings" }}
            />
          ),
        icon: <IqujiIcon decorative name={item.icon} />,
        id: item.id,
        label: item.label,
        onSelect: () => void navigate({ to: item.to }),
        tooltip: item.label,
      };
    });
  }, [navigate, pathname, navItems]);

  const titleBar = (
    <IqujiDesktopTitleBar className="ops-titlebar" dragRegion>
      {/* 左侧控制按钮群组 */}
      <div className="ops-titlebar-controls">
        <div className="ops-titlebar-history">
          <IconButton
            compact
            icon={<IqujiIcon decorative name="arrow-left" />}
            label={t("shell.back")}
            onClick={() => window.history.back()}
            variant="ghost"
          />
          <IconButton
            compact
            icon={<IqujiIcon decorative name="arrow-right" />}
            label={t("shell.forward")}
            onClick={() => window.history.forward()}
            variant="ghost"
          />
        </div>
        {hasMasterSidebar ? (
          <IconButton
            aria-expanded={!sidebarCollapsed}
            className="ops-titlebar-toggle-btn"
            compact
            icon={<IqujiIcon decorative name="panel-left" />}
            label={sidebarCollapsed ? t("shell.expandSidebar") : t("shell.collapseSidebar")}
            onClick={() => setSidebarCollapsed((prev) => !prev)}
            variant="secondary"
          />
        ) : null}
      </div>

      {/* 竖线分割符 */}
      {hasMasterSidebar ? (
        <div aria-hidden="true" className="ops-titlebar-divider" />
      ) : (
        <div style={{ width: 6 }} />
      )}

      {/* 工作区胶囊标签 */}
      <div className="ops-titlebar-tab-capsule">
        <IqujiIcon decorative name={currentNav.icon} />
        <span className="ops-titlebar-tab-title">
          {currentNav.id === "projects" ? t("nav.projectsTitle") : currentNav.label}
        </span>
      </div>

      {/* 中间弹性可拖拽空白区 */}
      <div className="ops-titlebar-spacer" />

      {/* 右侧状态与工具栏 */}
      <div className="ops-titlebar-actions">
        <TutorialMenu path={pathname} expand={setTourExpanded} />
        <span className="ops-titlebar-status-capsule">
          <StatusBadge tone="positive" />
          <span>{t("shell.controlPlaneHealthy")}</span>
        </span>
      </div>
    </IqujiDesktopTitleBar>
  );

  const rail = <IqujiDesktopRail items={railItems} label={t("nav.main")} showDivider={false} />;

  return (
    <ShellContext.Provider
      value={{ sidebarCollapsed: sidebarCollapsed && !tourExpanded, setSidebarCollapsed }}
    >
      <IqujiDesktopShell
        contentId="ops-main-content"
        rail={rail}
        skipLinkLabel={t("nav.skipToContent")}
        windowBar={titleBar}
      >
        <div className="ops-shell-main">
          <TutorialOverlay />
          <ActionGuideOverlay />
          <AutomationWindow />
          <Outlet />
          <OperationLogHost />
        </div>
      </IqujiDesktopShell>
    </ShellContext.Provider>
  );
}

function readCollapsed(): boolean {
  try {
    return window.localStorage.getItem(COLLAPSE_KEY) === "true";
  } catch {
    return false;
  }
}
