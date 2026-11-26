import { IqujiIcon } from "@iquji-community/web-icons";
import { useI18n } from "@/shared/i18n";

export function SwarmSummaryStrip({
  nodesCount,
  managerCount,
  workerCount,
  servicesCount,
  tasksCount,
  runningTaskCount,
  networksCount,
}: {
  nodesCount: number;
  managerCount: number;
  workerCount: number;
  servicesCount: number;
  tasksCount: number;
  runningTaskCount: number;
  networksCount: number;
}) {
  const { t } = useI18n();

  return (
    <div className="ops-swarm-summary-strip">
      <div className="ops-swarm-summary-item">
        <div className="ops-swarm-summary-item__icon" data-color="blue">
          <IqujiIcon decorative name="server" size={15} />
        </div>
        <div className="ops-swarm-summary-item__content">
          <span className="ops-swarm-summary-item__label">{t("swarm.tabNodes")}</span>
          <span className="ops-swarm-summary-item__value">
            {nodesCount}
            <span className="ops-swarm-summary-item__sub">
              ({managerCount} manager · {workerCount} worker)
            </span>
          </span>
        </div>
      </div>

      <div className="ops-swarm-summary-item">
        <div className="ops-swarm-summary-item__icon" data-color="purple">
          <IqujiIcon decorative name="boxes" size={15} />
        </div>
        <div className="ops-swarm-summary-item__content">
          <span className="ops-swarm-summary-item__label">{t("swarm.tabServices")}</span>
          <span className="ops-swarm-summary-item__value">
            {servicesCount}
            <span className="ops-swarm-summary-item__sub">
              {servicesCount > 0
                ? t("monitoring.registeredOrchestration")
                : t("monitoring.noServicesShort")}
            </span>
          </span>
        </div>
      </div>

      <div className="ops-swarm-summary-item">
        <div className="ops-swarm-summary-item__icon" data-color="emerald">
          <IqujiIcon decorative name="activity" size={15} />
        </div>
        <div className="ops-swarm-summary-item__content">
          <span className="ops-swarm-summary-item__label">{t("swarm.tabTasks")}</span>
          <span className="ops-swarm-summary-item__value">
            {tasksCount}
            <span className="ops-swarm-summary-item__sub">
              {t("monitoring.runningTasksCount", { count: runningTaskCount })}
            </span>
          </span>
        </div>
      </div>

      <div className="ops-swarm-summary-item">
        <div className="ops-swarm-summary-item__icon" data-color="teal">
          <IqujiIcon decorative name="network" size={15} />
        </div>
        <div className="ops-swarm-summary-item__content">
          <span className="ops-swarm-summary-item__label">{t("swarm.tabNetworks")}</span>
          <span className="ops-swarm-summary-item__value">
            {networksCount}
            <span className="ops-swarm-summary-item__sub">{t("monitoring.overlayNetwork")}</span>
          </span>
        </div>
      </div>
    </div>
  );
}
