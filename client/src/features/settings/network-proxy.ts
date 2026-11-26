import { invoke } from "@tauri-apps/api/core";

export type NetworkProxyMode = "direct" | "system" | "manual";
export type NetworkCredentialAction = "keep" | "replace" | "clear";

export type NetworkProxySettings = {
  mode: NetworkProxyMode;
  url: string | null;
  hasCredentials: boolean;
};

export type SaveNetworkProxySettings = {
  mode: NetworkProxyMode;
  url: string | null;
  credentials: {
    action: NetworkCredentialAction;
    username?: string;
    password?: string;
  };
};

export function desktopProxySettingsAvailable(): boolean {
  type TauriWindow = Window & { __TAURI_INTERNALS__?: { invoke?: unknown } };
  return (
    typeof window !== "undefined" &&
    typeof (window as TauriWindow).__TAURI_INTERNALS__?.invoke === "function"
  );
}

export function readNetworkProxySettings(): Promise<NetworkProxySettings> {
  return invoke<NetworkProxySettings>("network_proxy_settings");
}

export function saveNetworkProxySettings(
  input: SaveNetworkProxySettings,
): Promise<NetworkProxySettings> {
  return invoke<NetworkProxySettings>("save_network_proxy_settings", { input });
}
