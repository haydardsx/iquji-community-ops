import { IqujiField, IqujiInput, IqujiSelect } from "@iquji-community/web-component";
import { z } from "zod";
import type { Server } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { FieldWithHelp } from "@/shared/ui/field-help";
import { type SwarmConnectionMode, swarmAddressMode } from "../swarm-connection";

const ipAddress = z.string().ip();

export function swarmAddressError(
  mode: SwarmConnectionMode,
  address: string,
  t?: (key: string, params?: any) => string,
) {
  const value = address.trim();
  if (!value) return t ? t("servers.fillNodeIp") : "请填写其他节点能够访问的本节点 IP。";
  if (!ipAddress.safeParse(value).success)
    return t ? t("servers.validIpRequired") : "请输入有效的 IPv4 或 IPv6 地址。";
  const actualMode = swarmAddressMode(value);
  if (actualMode === "unknown")
    return t ? t("servers.unicastIpRequired") : "请填写可供其他节点连接的单播 IP。";
  if (actualMode !== mode) {
    const modeLabel =
      actualMode === "public"
        ? t
          ? t("servers.addressPublic")
          : "公网"
        : t
          ? t("servers.addressPrivate")
          : "私网";
    return t
      ? t("servers.ipModeMismatch", { mode: modeLabel })
      : `所填 IP 属于${actualMode === "public" ? "公网" : "私网"}，请调整通信网络或地址。`;
  }
  return undefined;
}

export function SwarmAddressFields({
  server,
  connectionMode,
  advertiseAddr,
  disabled,
  keepExisting = false,
  onConnectionModeChange,
  onAdvertiseAddrChange,
}: {
  server: Server;
  connectionMode: SwarmConnectionMode;
  advertiseAddr: string;
  disabled?: boolean;
  keepExisting?: boolean;
  onConnectionModeChange: (mode: SwarmConnectionMode) => void;
  onAdvertiseAddrChange: (value: string) => void;
}) {
  const { t } = useI18n();
  return (
    <>
      <IqujiField label={t("servers.clusterNetwork")}>
        <IqujiSelect
          disabled={disabled || keepExisting}
          value={connectionMode}
          options={[
            { value: "public", label: t("servers.addressPublic") },
            { value: "private", label: t("servers.addressPrivate") },
          ]}
          onValueChange={(value) => {
            if (value !== "public" && value !== "private") return;
            onConnectionModeChange(value);
            onAdvertiseAddrChange(
              value === "public" ? server.publicHost || "" : server.privateHost || "",
            );
          }}
        />
      </IqujiField>
      <FieldWithHelp
        label={t("servers.nodeAdvertiseIp")}
        help={keepExisting ? t("servers.keepExistingAddressDesc") : t("servers.newAddressDesc")}
        error={keepExisting ? undefined : swarmAddressError(connectionMode, advertiseAddr, t)}
        required={!keepExisting}
      >
        <IqujiInput
          disabled={disabled || keepExisting}
          value={advertiseAddr}
          onChange={(event) => onAdvertiseAddrChange(event.target.value)}
        />
      </FieldWithHelp>
    </>
  );
}
