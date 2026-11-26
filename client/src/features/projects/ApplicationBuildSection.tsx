import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { errorMessage, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { useOperationTracker } from "@/shared/operations/operation-tracker";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { useOpenOperationLog } from "@/shared/ui/operation-log-drawer";
import { RecordSnippet } from "@/shared/ui/record-snippet";
import { SectionPanel } from "@/shared/ui/section-panel";
import { useOperationError } from "@/shared/ui/use-operation-feedback";
import { ApplicationBuildArtifacts } from "./ApplicationBuildArtifacts";
import { BuildArtifactDrawer, type BuildArtifactSelection } from "./BuildArtifactDrawer";
import {
  type ApplicationBuild,
  cancelApplicationBuild,
  fetchApplicationBuilds,
  requestApplicationBuild,
  safeBuildURL,
} from "./build-api";
import { stateLabel, stateTone } from "./model";
import "./ApplicationSourceSection.css";
import "./ApplicationBuildSection.css";

const finished = new Set(["succeeded", "failed", "cancelled"]);

const buildPhases = new Set([
  "pending",
  "queued",
  "triggered",
  "executing",
  "verifying",
  "succeeded",
  "failed",
  "cancelled",
]);

function buildState(run: ApplicationBuild, t: (key: string) => string) {
  const status = run.status;
  const container = run.provider && run.provider !== "git";
  let phase = "projects.buildPhaseProcessing";
  if (container && (status === "running" || status === "executing")) {
    phase = "projects.containerPhaseDeploying";
  } else if (buildPhases.has(status)) {
    phase = `projects.buildPhase${status.charAt(0).toUpperCase() + status.slice(1)}`;
  }
  return { phase: t(phase), description: t(`${phase}Desc`) };
}

function technicalErrorCode(value?: string) {
  const code = value?.trim().split(":", 1)[0];
  return code && /^[A-Z][A-Z0-9_]+$/.test(code) ? code : undefined;
}

function buildResultMessage(run: ApplicationBuild, state: ReturnType<typeof buildState>) {
  const result = run.errorCode ?? run.summary;
  return result?.trim() ? operationErrorMessage(result) : state.description;
}

export function ApplicationBuildSection({
  action = "request_jenkins_release",
  applicationId,
  dirty,
  configured,
  visible,
  onChanged,
}: {
  action?: "request_jenkins_release" | "deploy_application";
  applicationId: string;
  dirty: boolean;
  configured: boolean;
  visible: boolean;
  onChanged: () => void;
}) {
  const { t, locale } = useI18n();
  const containerSource = action === "deploy_application";
  const historyLabel = t(containerSource ? "projects.releaseRecords" : "projects.buildRecords");
  const submitLabel = t(
    action === "deploy_application" ? "projects.deployApplication" : "projects.buildAndDeploy",
  );
  const [historyOpen, setHistoryOpen] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [cancel, setCancel] = useState<ApplicationBuild | null>(null);
  const [artifact, setArtifact] = useState<BuildArtifactSelection | null>(null);
  const operation = useConfirmedOperation();
  const openLog = useOpenOperationLog();
  const track = useOperationTracker();
  const builds = useQuery({
    queryKey: ["application-builds", applicationId],
    queryFn: () => fetchApplicationBuilds(applicationId),
    enabled: visible,
    refetchInterval: (query) =>
      visible && (historyOpen || query.state.data?.items.some((item) => !finished.has(item.status)))
        ? 5000
        : false,
  });
  const active = builds.data?.items.find((item) => !finished.has(item.status));
  const cancellation = useMutation({
    mutationFn: (run: ApplicationBuild) => cancelApplicationBuild(run.operationRunId),
    onSuccess: (run) => {
      track(run, t("projects.cancelThisRelease"));
      setCancel(null);
      void builds.refetch();
    },
  });
  useOperationError(cancellation.error, t("projects.cancelBuildFailed"));
  const submit = async () => {
    try {
      await operation.submit({
        action,
        targetKind: "application",
        targetId: applicationId,
        label: submitLabel,
        create: (requestKey) => requestApplicationBuild(applicationId, requestKey),
      });
      setConfirm(false);
      void builds.refetch();
      onChanged();
    } catch {
      /* 统一操作反馈保留原操作 ID，允许重试确认。 */
    }
  };
  const openHistory = () => {
    setHistoryOpen(true);
    void builds.refetch();
  };
  const buildCount = builds.data?.items.length ?? 0;
  const activeState = active ? buildState(active, t) : null;
  return (
    <SectionPanel
      className="ops-build-panel"
      icon="rocket"
      title={containerSource ? submitLabel : t("projects.buildSectionTitle")}
      description={t("projects.buildSectionDesc")}
      actions={
        <div className="ops-build-panel__actions">
          <IqujiButton
            compact
            variant="secondary"
            iconStart={<IqujiIcon decorative name="clock" size={14} />}
            onClick={openHistory}
          >
            <span className="ops-build-panel__records-label">
              <span>{historyLabel}</span>
              <OperationActivityDot scope={{ applicationId, action }} />
              {buildCount > 0 ? <span className="ops-build-panel__count">{buildCount}</span> : null}
            </span>
          </IqujiButton>
          <IqujiButton
            compact
            variant="primary"
            disabled={operation.isPending || (!operation.operationId && (dirty || !configured))}
            data-ops-guide-action="application_release"
            onClick={() => setConfirm(true)}
          >
            {operation.operationId ? t("projects.retryBuildConfirm") : submitLabel}
          </IqujiButton>
        </div>
      }
    >
      <div className="ops-build-panel__body">
        {active && activeState ? (
          <div className="ops-build-status-banner" data-tone="active">
            <div className="ops-build-status-banner__main">
              <span className="ops-build-status-banner__icon-badge" data-tone="active">
                <IqujiIcon decorative name="loader" size={18} />
              </span>
              <div className="ops-build-status-banner__copy">
                <div className="ops-build-status-banner__headline">
                  <strong className="ops-build-status-banner__title">{activeState.phase}</strong>
                  <IqujiBadge tone={stateTone(active.status)}>
                    {stateLabel(active.status, t)}
                  </IqujiBadge>
                </div>
                <p className="ops-build-status-banner__text">{t("operations.activeBuildQueue")}</p>
              </div>
            </div>
          </div>
        ) : null}

        {dirty ? (
          <div className="ops-build-status-banner" data-tone="warning">
            <div className="ops-build-status-banner__main">
              <span className="ops-build-status-banner__icon-badge" data-tone="warning">
                <IqujiIcon decorative name="circle-alert" size={18} />
              </span>
              <div className="ops-build-status-banner__copy">
                <strong className="ops-build-status-banner__title">
                  {t("projects.saveFirstTitle")}
                </strong>
                <p className="ops-build-status-banner__text">{t("projects.saveFirstPrompt")}</p>
              </div>
            </div>
          </div>
        ) : null}

        {!dirty && !active && !configured ? (
          <div className="ops-build-status-banner" data-tone="muted">
            <div className="ops-build-status-banner__main">
              <span className="ops-build-status-banner__icon-badge" data-tone="muted">
                <IqujiIcon decorative name="circle-info" size={18} />
              </span>
              <div className="ops-build-status-banner__copy">
                <strong className="ops-build-status-banner__title">
                  {t("projects.incompleteConfigTitle")}
                </strong>
                <p className="ops-build-status-banner__text">
                  {t("projects.incompleteConfigPrompt")}
                </p>
              </div>
            </div>
          </div>
        ) : null}

        {!dirty && !active && configured ? (
          <div className="ops-build-status-banner" data-tone="ready">
            <div className="ops-build-status-banner__main">
              <span className="ops-build-status-banner__icon-badge" data-tone="ready">
                <IqujiIcon decorative name="check-circle" size={18} />
              </span>
              <div className="ops-build-status-banner__copy">
                <strong className="ops-build-status-banner__title">
                  {t("projects.readyToBuildTitle")}
                </strong>
                <p className="ops-build-status-banner__text">{t("projects.readyToBuildPrompt")}</p>
              </div>
            </div>
            <div className="ops-build-status-banner__meta">
              <span className="ops-build-status-banner__chip">
                <IqujiIcon decorative name={containerSource ? "container" : "code"} size={13} />
                {containerSource ? t("projects.imageSource") : t("projects.sourceGit")}
              </span>
              <span className="ops-build-status-banner__chip">
                <IqujiIcon decorative name="server" size={13} />
                {t("projects.targetCluster")}
              </span>
            </div>
          </div>
        ) : null}
      </div>
      <InspectorDrawer
        open={historyOpen}
        onClose={() => setHistoryOpen(false)}
        title={historyLabel}
        size="lg"
        headerToolbar={
          <div className="ops-build-history__toolbar">
            <div className="ops-build-history__counter">
              <span className="ops-muted ops-text-caption">
                {t("projects.buildCountLabel", { count: builds.data?.items.length ?? 0 })}
              </span>
            </div>
            <IqujiButton
              compact
              variant="ghost"
              iconStart={<IqujiIcon decorative name="refresh" />}
              loading={builds.isFetching}
              onClick={() => void builds.refetch()}
            >
              {t("projects.refreshRecords")}
            </IqujiButton>
          </div>
        }
      >
        {builds.isLoading ? <IqujiLoadingState label={t("projects.readingRecords")} /> : null}
        {builds.error ? (
          <IqujiAlert title={t("projects.readRecordsFailed")} tone="danger">
            {errorMessage(builds.error)}
          </IqujiAlert>
        ) : null}
        {builds.data?.items.length === 0 ? (
          <div className="ops-build-empty">
            <div className="ops-build-empty__icon">
              <IqujiIcon decorative name="boxes" size={32} />
            </div>
            <h4 className="ops-build-empty__title">
              {containerSource ? t("projects.noReleaseRecords") : t("projects.noBuildRecords")}
            </h4>
            <p className="ops-build-empty__desc">
              {t("projects.firstDeployPrompt", { action: submitLabel })}
            </p>
          </div>
        ) : null}
        <div className="ops-build-stream">
          {builds.data?.items.map((run) => {
            const state = buildState(run, t);
            const result = run.errorCode ?? run.summary;
            const code = technicalErrorCode(result);
            const isFinished = finished.has(run.status);
            const artifacts = (
              [
                [t("projects.buildDetail"), run.runUrl, "summary"],
                [t("projects.stageDetail"), run.stageUrl, "stage"],
                [t("projects.jenkinsLog"), run.consoleUrl, "console"],
              ] as const
            ).flatMap(([label, url, kind]) => {
              const href = safeBuildURL(url);
              return href ? [{ label, url: href, kind, operationId: run.operationRunId }] : [];
            });
            const consoleArtifact = artifacts.find((item) => item.kind === "console");
            const secondaryArtifacts = artifacts.filter((item) => item.kind !== "console");

            let statusIcon: IqujiIconName = "clock";
            if (run.status === "succeeded") {
              statusIcon = "check";
            } else if (run.status === "failed") {
              statusIcon = "circle-alert";
            } else if (run.status === "cancelled") {
              statusIcon = "minus";
            } else {
              statusIcon = "loader";
            }

            const resultMsg = buildResultMessage(run, state);

            return (
              <article className="ops-build-card" key={run.id} data-status={run.status}>
                <div className="ops-build-card__head">
                  <div className="ops-build-card__icon-wrap" data-status={run.status}>
                    <IqujiIcon decorative name={statusIcon} size={18} />
                  </div>
                  <div className="ops-build-card__title-col">
                    <div className="ops-build-card__title-row">
                      <strong className="ops-build-card__title">{state.phase}</strong>
                      <div className="ops-build-card__title-actions">
                        <IqujiBadge tone={stateTone(run.status)}>
                          {stateLabel(run.status, t)}
                        </IqujiBadge>
                        {run.operationRunId ? (
                          <IqujiButton
                            compact
                            variant="secondary"
                            className="ops-build-card__log-button"
                            iconStart={<IqujiIcon decorative name="terminal" size={13} />}
                            onClick={() => {
                              if (consoleArtifact) {
                                setArtifact(consoleArtifact);
                              } else {
                                openLog(run.operationRunId);
                              }
                            }}
                          >
                            {consoleArtifact ? t("projects.jenkinsLog") : t("operations.logs")}
                            <OperationActivityDot operationId={run.operationRunId} />
                          </IqujiButton>
                        ) : null}
                      </div>
                    </div>
                    <div className="ops-build-card__sub-row">
                      <IqujiIcon decorative name="clock" size={12} />
                      <span className="ops-build-card__time">
                        {new Date(run.createdAt).toLocaleString(locale)}
                      </span>
                    </div>
                  </div>
                </div>

                {resultMsg ? (
                  <div className="ops-build-card__callout" data-status={run.status}>
                    <p className="ops-build-card__result">{resultMsg}</p>
                    {run.status === "failed" && code ? (
                      <div className="ops-build-card__code-wrap">
                        <code className="ops-build-card__code">{code}</code>
                      </div>
                    ) : null}
                  </div>
                ) : null}

                <div className="ops-build-card__meta-grid">
                  {run.pullMirrors?.map((mirror) => (
                    <div className="ops-build-card__meta-item" key={mirror.serverId}>
                      <span className="ops-build-card__meta-label">
                        {t("projects.pullMirror")} · {t(`projects.mirrorRole.${mirror.role}`)}
                      </span>
                      <div className="ops-build-card__meta-value">
                        {mirror.serverId} · {t(`projects.mirrorOrigin.${mirror.origin}`)} ·{" "}
                        {mirror.addresses.join(", ") ||
                          t(mirror.probed ? "projects.mirrorDefault" : "projects.mirrorUnprobed")}
                      </div>
                    </div>
                  ))}
                  {(run.commitSha || run.consoleUrl) && (
                    <div className="ops-build-card__meta-item">
                      <span className="ops-build-card__meta-label">
                        <IqujiIcon decorative name="code" size={13} />
                        {t("projects.sourceCommit")}
                      </span>
                      <div className="ops-build-card__meta-value">
                        <RecordSnippet
                          value={run.commitSha}
                          label={t("projects.sourceCommit")}
                          truncateLength={12}
                        />
                      </div>
                    </div>
                  )}
                  {!run.artifacts?.length && run.imageDigest && (
                    <div className="ops-build-card__meta-item">
                      <span className="ops-build-card__meta-label">
                        <IqujiIcon decorative name="container" size={13} />
                        {t("projects.imageDigest")}
                      </span>
                      <div className="ops-build-card__meta-value">
                        <RecordSnippet
                          value={run.imageDigest}
                          label={t("projects.imageDigest")}
                          truncateLength={16}
                        />
                      </div>
                    </div>
                  )}

                  <div className="ops-build-card__meta-item">
                    <span className="ops-build-card__meta-label">
                      <IqujiIcon decorative name="clipboard" size={13} />
                      {t("projects.releaseNumber")}
                    </span>
                    <div className="ops-build-card__meta-value">
                      <RecordSnippet
                        value={run.operationRunId}
                        label={t("projects.releaseNumber")}
                        truncateLength={24}
                      />
                    </div>
                  </div>
                </div>

                <ApplicationBuildArtifacts artifacts={run.artifacts} />
                {run.operationRunId && (secondaryArtifacts.length > 0 || !isFinished) ? (
                  <div className="ops-build-card__actions">
                    <div className="ops-build-card__action-group">
                      {secondaryArtifacts.map((item) => (
                        <IqujiButton
                          key={item.kind}
                          compact
                          variant="secondary"
                          iconStart={<IqujiIcon decorative name="file-search" size={12} />}
                          onClick={() => setArtifact(item)}
                        >
                          {item.label}
                        </IqujiButton>
                      ))}
                    </div>
                    {!isFinished ? (
                      <IqujiButton
                        compact
                        variant="danger"
                        disabled={cancellation.isPending}
                        iconStart={<IqujiIcon decorative name="close" size={12} />}
                        onClick={() => setCancel(run)}
                      >
                        {t("projects.cancelThisRelease")}
                      </IqujiButton>
                    ) : null}
                  </div>
                ) : null}
              </article>
            );
          })}
        </div>
      </InspectorDrawer>
      <IqujiConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        closeLabel={t("projects.closeBuildConfirm")}
        title={
          containerSource ? t("projects.confirmReleaseTitle") : t("projects.confirmBuildTitle")
        }
        description={t("projects.confirmBuildDesc")}
        cancelLabel={t("common.cancel")}
        confirmLabel={operation.operationId ? t("common.retry") : submitLabel}
        confirmLoading={operation.isPending}
        onConfirm={() => void submit()}
        additionalActions={
          operation.operationId ? (
            <IqujiButton
              compact
              variant="secondary"
              onClick={() =>
                void operation
                  .cancelPending()
                  .then(() => setConfirm(false))
                  .catch(() => {})
              }
            >
              {t("projects.cancelOldOperation")}
            </IqujiButton>
          ) : undefined
        }
      />
      <IqujiConfirmDialog
        open={cancel !== null}
        onOpenChange={(open) => {
          if (!open) setCancel(null);
        }}
        closeLabel={t("projects.closeCancelBuildConfirm")}
        title={t("projects.confirmCancelBuildTitle")}
        description={t("projects.confirmCancelBuildDesc")}
        cancelLabel={t("common.back")}
        confirmLabel={t("projects.cancelBuild")}
        confirmLoading={cancellation.isPending}
        onConfirm={() => {
          if (cancel) cancellation.mutate(cancel);
        }}
      />
      {artifact ? (
        <BuildArtifactDrawer artifact={artifact} onClose={() => setArtifact(null)} />
      ) : null}
    </SectionPanel>
  );
}
