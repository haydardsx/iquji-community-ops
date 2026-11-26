import { fetchJson } from "@/shared/api/client";
export function windowRequest<T>(action: string, body: unknown = {}, signal?: AbortSignal) {
  return fetchJson<T>(`automation/window/${action}`, { method: "POST", body, signal });
}
