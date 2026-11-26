import { invoke } from "@tauri-apps/api/core";
import { useEffect } from "react";
import { isTauriDesktop } from "./external-url";

const editable =
  'input, textarea, [contenteditable="true"], [contenteditable="plaintext-only"], [contenteditable=""]';
const dirtyForms = new Set<symbol>();
let confirmedUnload = false;

export function useWorkspaceUnloadGuard(dirty: boolean) {
  useEffect(() => {
    if (!dirty) return;
    const form = Symbol();
    dirtyForms.add(form);
    return () => {
      dirtyForms.delete(form);
    };
  }, [dirty]);
  return () => dirty && !confirmedUnload;
}

export function workspaceHasDrafts() {
  return dirtyForms.size > 0;
}

export function listenToWorkspace(onReload: (ignoreCache: boolean) => void) {
  const controller = new AbortController();
  const options = { capture: true, signal: controller.signal };
  // 若宿主取消了导航，下一次用户操作或历史恢复仍需保护表单。
  const restoreGuard = () => {
    confirmedUnload = false;
  };
  window.addEventListener("pageshow", restoreGuard, options);
  window.addEventListener("pointerdown", restoreGuard, options);
  window.addEventListener("contextmenu", (event) => event.preventDefault(), options);
  window.addEventListener(
    "selectstart",
    (event) => {
      const inEditor = event
        .composedPath()
        .some((node) => node instanceof Element && node.matches(editable));
      if (!inEditor) event.preventDefault();
    },
    options,
  );
  window.addEventListener(
    "keydown",
    (event) => {
      restoreGuard();
      if (
        event.defaultPrevented ||
        event.isComposing ||
        !event.ctrlKey ||
        event.metaKey ||
        event.altKey ||
        event.key.toLowerCase() !== "r"
      )
        return;
      // Windows 和 Linux 浏览器原生处理这两组按键。
      if (!isTauriDesktop() && !/Mac|iPhone|iPad/.test(navigator.platform)) return;
      event.preventDefault();
      if (!event.repeat) onReload(event.shiftKey);
    },
    options,
  );
  return () => controller.abort();
}

export async function reloadWorkspace(ignoreCache: boolean, discardDrafts: boolean) {
  const desktop = isTauriDesktop();
  if (ignoreCache && !desktop) {
    // macOS 浏览器的原生强刷使用 ⌘。Control 组合键重新获取当前文档和已载入的同源打包资源。
    const urls = new Set([window.location.href.split("#")[0]]);
    for (const entry of performance.getEntriesByType("resource")) {
      const url = new URL(entry.name, window.location.href);
      if (url.origin === window.location.origin && url.pathname.startsWith("/assets/"))
        urls.add(url.href);
    }
    // 旧版本的资源可能已经移除，仍须进入新文档，加载该版本引用的资源。
    await Promise.allSettled(
      Array.from(urls, async (url) => {
        const response = await fetch(url, { cache: "reload" });
        await response.arrayBuffer();
      }),
    );
  }
  confirmedUnload = discardDrafts;
  try {
    if (desktop) await invoke("reload_webview", { ignoreCache });
    else window.location.reload();
  } catch (error) {
    confirmedUnload = false;
    throw error;
  }
}
