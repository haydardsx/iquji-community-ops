import { describe, expect, it } from "vitest";
import { probeCurl } from "./TunnelProbeDetailDrawer";

describe("探针请求命令", () => {
  it("保留 URL 的字面值", () => {
    expect(probeCurl("GET", "api.example.test", "/docs/reader's-guide?cost=$1")).toBe(
      "curl --globoff -X GET -- 'http://api.example.test/docs/reader'\\''s-guide?cost=$1'",
    );
  });

  it("地址缺失、带有身份信息或方法无效时停止复制", () => {
    expect(probeCurl("GET", "", "/docs")).toBe("");
    expect(probeCurl("GET", "user@api.example.test", "/docs")).toBe("");
    expect(probeCurl("BAD METHOD", "api.example.test", "/docs")).toBe("");
  });
});
