import { fetchJson } from "@/shared/api/client";

export type ServerConnectionProxy = {
  mode: "direct" | "manual";
  url?: string;
  hasCredentials?: boolean;
};
type ProxyCredentials =
  | { action: "keep" | "clear" }
  | { action: "replace"; username: string; password: string };
type HostIdentity = { fingerprint: string; trusted: boolean; previousFingerprint: string };

export function saveServerConnection(
  id: string,
  input: { mode: ServerConnectionProxy["mode"]; url?: string; credentials: ProxyCredentials },
) {
  return fetchJson<ServerConnectionProxy>(`servers/${encodeURIComponent(id)}/connection`, {
    method: "PUT",
    body: input,
  });
}
export function testServerConnection(id: string) {
  return fetchJson<HostIdentity>(`servers/${encodeURIComponent(id)}/connection/test`, {
    method: "POST",
    body: {},
  });
}
export function trustServerConnection(id: string, fingerprint: string) {
  return fetchJson<HostIdentity>(`servers/${encodeURIComponent(id)}/connection/trust`, {
    method: "POST",
    body: { fingerprint },
  });
}
