import { afterEach, describe, expect, it, vi } from "vitest";

const invokeMock = vi.fn();

vi.mock("@tauri-apps/api/core", () => ({
  invoke: (...args: unknown[]) => invokeMock(...args),
}));

import { isValidExternalHttpUrl, openExternalUrl } from "./external-url";

type MutableWindow = {
  __TAURI_INTERNALS__?: { invoke?: unknown };
  open?: (...args: unknown[]) => unknown;
};

describe("外链打开工具", () => {
  const originalWindow = globalThis.window;

  afterEach(() => {
    invokeMock.mockReset();
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      writable: true,
      value: originalWindow,
    });
  });

  it("仅放行合法的 http/https 地址", () => {
    expect(isValidExternalHttpUrl("http://198.51.100.10:8080/probe")).toBe(true);
    expect(isValidExternalHttpUrl("https://example.test/path?q=1")).toBe(true);
    expect(isValidExternalHttpUrl("javascript:alert(1)")).toBe(false);
    expect(isValidExternalHttpUrl("file:///etc/hosts")).toBe(false);
    expect(isValidExternalHttpUrl(" https://example.test")).toBe(false);
  });

  it("在 Tauri 桌面环境中通过受控命令打开系统浏览器", async () => {
    invokeMock.mockResolvedValueOnce(undefined);
    const mockWindow: MutableWindow = {
      __TAURI_INTERNALS__: { invoke: vi.fn() },
      open: vi.fn(),
    };
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      writable: true,
      value: mockWindow,
    });

    const opened = await openExternalUrl("https://example.test/app");
    expect(opened).toBe(true);
    expect(invokeMock).toHaveBeenCalledWith("open_external_url", {
      url: "https://example.test/app",
    });
    expect(mockWindow.open).not.toHaveBeenCalled();
  });

  it("在普通浏览器环境中回退 window.open", async () => {
    const openSpy = vi.fn();
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      writable: true,
      value: { open: openSpy } satisfies MutableWindow,
    });

    const opened = await openExternalUrl("http://198.51.100.10");
    expect(opened).toBe(true);
    expect(openSpy).toHaveBeenCalledWith("http://198.51.100.10", "_blank", "noopener,noreferrer");
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it("拒绝非法协议且不触发任何打开调用", async () => {
    const openSpy = vi.fn();
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      writable: true,
      value: { open: openSpy } satisfies MutableWindow,
    });

    const opened = await openExternalUrl("javascript:alert(1)");
    expect(opened).toBe(false);
    expect(openSpy).not.toHaveBeenCalled();
    expect(invokeMock).not.toHaveBeenCalled();
  });
});
