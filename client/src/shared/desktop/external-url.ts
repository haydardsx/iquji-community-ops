import { invoke } from "@tauri-apps/api/core";

type TauriWindow = Window & { __TAURI_INTERNALS__?: { invoke?: unknown } };

export function isTauriDesktop(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof (window as TauriWindow).__TAURI_INTERNALS__?.invoke === "function"
  );
}

export function isValidExternalHttpUrl(raw: string): boolean {
  const trimmed = raw.trim();
  if (!trimmed || trimmed !== raw) return false;
  try {
    const parsed = new URL(trimmed);
    return parsed.protocol === "http:" || parsed.protocol === "https:";
  } catch {
    return false;
  }
}

/** 统一打开 http/https 外链：桌面端走受控 Tauri 命令拉起系统浏览器，Web 端回退 window.open。 */
export async function openExternalUrl(url: string): Promise<boolean> {
  if (!isValidExternalHttpUrl(url)) return false;
  if (isTauriDesktop()) {
    try {
      await invoke("open_external_url", { url });
      return true;
    } catch {
      return false;
    }
  }
  if (typeof window === "undefined") return false;
  window.open(url, "_blank", "noopener,noreferrer");
  return true;
}
