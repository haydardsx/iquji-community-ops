import { beforeEach, describe, expect, it, vi } from "vitest";

const { transport } = vi.hoisted(() => ({ transport: vi.fn() }));
vi.mock("@iquji-community/web-network", () => ({ createHttpTransport: () => transport }));

import { fetchJson } from "./client";

beforeEach(() => {
  transport
    .mockReset()
    .mockImplementation(
      async () => new Response("{}", { headers: { "content-type": "application/json" } }),
    );
});

describe("操作请求幂等标识", () => {
  it("并发任务各自携带调用者的 key，不通过全局状态串用", async () => {
    await Promise.all([
      fetchJson("servers/server_one/docker/install", {
        method: "POST",
        body: {},
        requestKey: "request_one",
      }),
      fetchJson("servers/server_two/docker/install", {
        method: "POST",
        body: {},
        requestKey: "request_two",
      }),
    ]);
    expect(new Headers(transport.mock.calls[0][1].headers).get("Idempotency-Key")).toBe(
      "request_one",
    );
    expect(new Headers(transport.mock.calls[1][1].headers).get("Idempotency-Key")).toBe(
      "request_two",
    );
  });

  it("只读请求不会继承之前任务的 key", async () => {
    await fetchJson("operations/activity");
    expect(new Headers(transport.mock.calls[0][1].headers).has("Idempotency-Key")).toBe(false);
  });
});
