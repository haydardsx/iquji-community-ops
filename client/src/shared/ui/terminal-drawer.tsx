import { IqujiButton, IqujiDrawer, IqujiTerminal } from "@iquji-community/web-component";
import { createTerminalConnection } from "@iquji-community/web-network";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { ApplicationRuntime, Server } from "@/contracts/types";
import { testServerConnection, trustServerConnection } from "@/features/servers/connection-api";
import { errorMessage, fetchJson } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";

type Ticket = { ticket: string; serverId: string };
type Target = { id: string; kind: "application" | "server"; taskId?: string };
type Identity = { fingerprint: string; trusted: boolean; previousFingerprint: string };
const issue = (target: Target, signal?: AbortSignal) =>
  fetchJson<Ticket>("servers/terminal-tickets", { method: "POST", body: target, signal });

function TerminalConnection({ id, kind, taskId, retry }: Target & { retry: () => void }) {
  const { t } = useI18n();
  const [identity, setIdentity] = useState<{ serverId: string; host: Identity }>();
  const [ticket, setTicket] = useState<string>();
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    const target = { id, kind, taskId };
    setTicket(undefined);
    setIdentity(undefined);
    setStatus("");
    void (async () => {
      const entry = await issue(target, controller.signal);
      const host = await testServerConnection(entry.serverId);
      if (controller.signal.aborted) return;
      if (host.trusted) setTicket(entry.ticket);
      else setIdentity({ serverId: entry.serverId, host });
    })().catch((err) => {
      if (!controller.signal.aborted) setStatus(errorMessage(err, t));
    });
    return () => controller.abort();
  }, [id, kind, taskId, t]);
  const trust = async () => {
    if (!identity) return;
    setBusy(true);
    try {
      await trustServerConnection(identity.serverId, identity.host.fingerprint);
      retry();
    } catch (err) {
      setStatus(errorMessage(err, t));
    } finally {
      setBusy(false);
    }
  };
  const connect = useMemo(
    () =>
      ticket
        ? createTerminalConnection("/api/servers/terminal-stream", { ticket }, (event) => {
            const code = event.reason || (event.code === 1000 ? "" : "TERMINAL_CONNECTION_FAILED");
            setStatus(code ? errorMessage(new Error(code), t) : t("terminal.ended"));
          })
        : undefined,
    [ticket, t],
  );
  const fail = useCallback(() => setStatus(t("terminal.failed")), [t]);
  return (
    <div className="ops-stack">
      {identity && !identity.host.trusted && (
        <div className="ops-stack">
          <p>{identity.host.previousFingerprint ? t("terminal.changed") : t("terminal.verify")}</p>
          <code>{identity.host.fingerprint}</code>
          {!identity.host.previousFingerprint && (
            <IqujiButton disabled={busy} onClick={() => void trust()}>
              {t("terminal.trust")}
            </IqujiButton>
          )}
        </div>
      )}
      {connect && (
        <IqujiTerminal
          label={t(kind === "server" ? "terminal.server" : "terminal.container")}
          connect={connect}
          onError={fail}
        />
      )}
      {!ticket && !identity && !status && <span role="status">{t("terminal.connecting")}</span>}
      {status && (
        <div role="status" className="ops-row">
          {status}
          <IqujiButton variant="secondary" onClick={retry}>
            {t("common.retry")}
          </IqujiButton>
        </div>
      )}
    </div>
  );
}

export interface TerminalDrawerProps {
  id: string;
  kind: Target["kind"];
  name: string;
  runtime?: ApplicationRuntime;
  servers?: Server[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function TerminalDrawer({
  id,
  kind,
  name,
  runtime,
  servers,
  open,
  onOpenChange,
}: TerminalDrawerProps) {
  const { t } = useI18n();
  const [attempt, setAttempt] = useState(0);
  const [taskId, setTaskId] = useState<string>();
  const tasks = (runtime?.services ?? []).flatMap((service) =>
    service.tasks
      .filter((task) => task.status === "running")
      .map((task) => ({
        id: task.id,
        label: [
          service.serviceName,
          servers?.find((s) => s.id === task.serverId)?.name ?? task.nodeId,
          task.id.slice(0, 12),
        ].join(" · "),
      })),
  );

  const singleTaskId = tasks.length === 1 ? tasks[0]?.id : undefined;
  useEffect(() => {
    if (open) {
      setTaskId(singleTaskId);
    }
  }, [open, singleTaskId]);

  const title = t(kind === "server" ? "terminal.server" : "terminal.container");

  if (!open) return null;

  return (
    <IqujiDrawer
      open
      title={title}
      description={name}
      closeLabel={t("common.close")}
      onOpenChange={onOpenChange}
    >
      {kind === "server" || taskId ? (
        <TerminalConnection
          key={`${taskId ?? id}-${attempt}`}
          kind={kind}
          id={id}
          taskId={taskId}
          retry={() => setAttempt((n) => n + 1)}
        />
      ) : (
        <div className="ops-stack">
          {tasks.length ? (
            tasks.map((task) => (
              <IqujiButton key={task.id} variant="secondary" onClick={() => setTaskId(task.id)}>
                {task.label}
              </IqujiButton>
            ))
          ) : (
            <span>
              {runtime?.unavailableReason
                ? errorMessage(new Error(runtime.unavailableReason), t)
                : t("terminal.empty")}
            </span>
          )}
        </div>
      )}
    </IqujiDrawer>
  );
}

export function TerminalLauncher(props: {
  id: string;
  kind: Target["kind"];
  name: string;
  runtime?: ApplicationRuntime;
  servers?: Server[];
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const title = t(props.kind === "server" ? "terminal.server" : "terminal.container");

  return (
    <>
      <IqujiButton variant="secondary" compact onClick={() => setOpen(true)}>
        {title}
      </IqujiButton>
      <TerminalDrawer {...props} open={open} onOpenChange={setOpen} />
    </>
  );
}
