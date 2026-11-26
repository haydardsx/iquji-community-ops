import type { OperationRun } from "@/contracts/types";
import { fetchJson } from "@/shared/api/client";

export type JenkinsAccess = {
  status: "unbound" | "active" | "pending" | "failed";
  ready: boolean;
  domainId?: string;
  gatewayServerId?: string;
  url?: string;
  viewerUsername?: string;
  version?: string;
  checkedAt?: string;
};

const endpoint = (serverId: string) => `servers/${encodeURIComponent(serverId)}/jenkins/access`;
export const fetchJenkinsAccess = (serverId: string) =>
  fetchJson<JenkinsAccess>(endpoint(serverId));
export function bindJenkinsAccess(
  serverId: string,
  body: { domainId: string; gatewayServerId: string; viewerPassword: string },
  requestKey: string,
) {
  return fetchJson<OperationRun>(endpoint(serverId), { method: "POST", requestKey, body });
}
export function unbindJenkinsAccess(serverId: string, requestKey: string) {
  return fetchJson<OperationRun>(endpoint(serverId), { method: "DELETE", requestKey });
}

export function validViewerPassword(value: string) {
  return (
    Array.from(value).length >= 12 &&
    new TextEncoder().encode(value).length <= 128 &&
    Array.from(value).every(
      (char) => (char.codePointAt(0) ?? 0) >= 32 && !/[\u007f-\u009f]/u.test(char),
    )
  );
}

export function verifiedJenkinsURL(access?: JenkinsAccess) {
  if (access?.status !== "active" || !access.url) return "";
  try {
    const url = new URL(access.url);
    return url.protocol === "https:" && !url.username && !url.password && !url.search && !url.hash
      ? url.href
      : "";
  } catch {
    return "";
  }
}
