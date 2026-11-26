import {
  IqujiAlert,
  IqujiButton,
  IqujiField,
  IqujiInput,
  IqujiSelect,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import type { Server } from "@/contracts/types";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import {
  saveServerConnection,
  testServerConnection,
  trustServerConnection,
} from "./connection-api";

export function ServerConnectionDrawer({
  server,
  onClose,
  onSaved,
}: {
  server: Server;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const notify = useIqujiNotification();
  const [saved, setSaved] = useState(server.connectionProxy ?? { mode: "direct" as const });
  const [mode, setMode] = useState(saved.mode);
  const [url, setUrl] = useState(saved.url ?? "");
  const [action, setAction] = useState<"keep" | "clear" | "replace">("keep");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [identity, setIdentity] = useState<Awaited<ReturnType<typeof testServerConnection>>>();
  const [failure, setFailure] = useState("");
  const changed =
    mode !== saved.mode || (mode === "manual" && (url !== saved.url || action !== "keep"));
  const work = useMutation({
    mutationFn: async (task: "save" | "test" | "trust") => {
      setFailure("");
      if (task === "save") {
        const credentials =
          mode === "direct"
            ? { action: "clear" as const }
            : action === "replace"
              ? { action, username, password }
              : { action };
        const next = await saveServerConnection(server.id, {
          mode,
          url: mode === "manual" ? url.trim() : undefined,
          credentials,
        });
        setSaved(next);
        setUrl(next.url ?? "");
        setAction("keep");
        setPassword("");
        setUsername("");
        setIdentity(undefined);
        onSaved();
        notify.open({ title: t("network.proxySaved"), tone: "success" });
        onClose();
      } else if (task === "test") {
        setIdentity(undefined);
        setIdentity(await testServerConnection(server.id));
      } else if (identity) {
        setIdentity(await trustServerConnection(server.id, identity.fingerprint));
        onSaved();
      }
    },
    onError: (error) => setFailure(errorMessage(error, t)),
  });
  const disabled = work.isPending;
  return (
    <InspectorDrawer
      open
      size="md"
      title={`${t("servers.connectionProxy")} · ${server.name}`}
      onClose={onClose}
      footer={
        <>
          <IqujiButton
            variant="secondary"
            disabled={changed || disabled}
            onClick={() => work.mutate("test")}
          >
            {t("servers.connectionTest")}
          </IqujiButton>
          <IqujiButton
            disabled={
              disabled ||
              !changed ||
              (mode === "manual" &&
                (!url.trim() || (action === "replace" && (!username || !password))))
            }
            onClick={() => work.mutate("save")}
          >
            {t("common.save")}
          </IqujiButton>
        </>
      }
    >
      <div className="ops-form-stack">
        <IqujiField label={t("network.proxyMode")}>
          <IqujiSelect
            disabled={disabled}
            value={mode}
            options={[
              { value: "direct", label: t("network.proxyDirect") },
              { value: "manual", label: t("network.proxyManual") },
            ]}
            onValueChange={(v) => setMode(v === "manual" ? "manual" : "direct")}
          />
        </IqujiField>
        {mode === "manual" && (
          <>
            <IqujiField label={t("network.proxyUrl")} description={t("servers.connectionSchemes")}>
              <IqujiInput
                disabled={disabled}
                value={url}
                onChange={(e) => setUrl(e.currentTarget.value)}
              />
            </IqujiField>
            <IqujiField label={t("network.proxyAuth")}>
              <IqujiSelect
                disabled={disabled}
                value={action}
                options={[
                  {
                    value: "keep",
                    label: saved.hasCredentials
                      ? t("network.proxyAuthKeep")
                      : t("servers.connectionNoAuth"),
                  },
                  { value: "replace", label: t("network.proxyAuthReplace") },
                  ...(saved.hasCredentials
                    ? [{ value: "clear", label: t("network.proxyAuthClear") }]
                    : []),
                ]}
                onValueChange={(v) => setAction(v === "clear" || v === "replace" ? v : "keep")}
              />
            </IqujiField>
            {action === "replace" && (
              <>
                <IqujiField label={t("network.username")}>
                  <IqujiInput
                    disabled={disabled}
                    autoComplete="off"
                    value={username}
                    onChange={(e) => setUsername(e.currentTarget.value)}
                  />
                </IqujiField>
                <IqujiField label={t("network.password")}>
                  <IqujiInput
                    disabled={disabled}
                    autoComplete="new-password"
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.currentTarget.value)}
                  />
                </IqujiField>
              </>
            )}
          </>
        )}
        {identity && !changed && (
          <IqujiAlert
            tone={identity.trusted ? "success" : "warning"}
            title={
              identity.trusted ? t("servers.connectionTrusted") : t("servers.connectionVerify")
            }
          >
            {identity.previousFingerprint &&
              identity.previousFingerprint !== identity.fingerprint && (
                <p>
                  {t("servers.connectionPrevious")} <code>{identity.previousFingerprint}</code>
                </p>
              )}
            <p>
              <code>{identity.fingerprint}</code>
            </p>
            {!identity.trusted && (
              <IqujiButton
                disabled={disabled}
                variant="secondary"
                onClick={() => work.mutate("trust")}
              >
                {t("servers.connectionTrust")}
              </IqujiButton>
            )}
          </IqujiAlert>
        )}
        {failure && (
          <IqujiAlert title={t("common.opFailed")} tone="danger">
            {failure}
          </IqujiAlert>
        )}
      </div>
    </InspectorDrawer>
  );
}
