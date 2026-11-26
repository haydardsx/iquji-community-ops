import { invoke } from "@tauri-apps/api/core";
import { isTauriDesktop } from "./external-url";

let session: Promise<string> | undefined;

export async function desktopSession() {
  if (!isTauriDesktop()) return undefined;
  session ??= invoke<string>("automation_window_key").catch((error: unknown) => {
    session = undefined;
    throw error;
  });
  return session;
}
