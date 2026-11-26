import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { RecordSnippet } from "./record-snippet";

describe("RecordSnippet", () => {
  it("无值时显示空态破折号", () => {
    const markup = renderToStaticMarkup(<RecordSnippet value="" />);
    expect(markup).toContain("—");
  });

  it("长哈希智能截短且包含复制按钮与完整 title", () => {
    const hash = "task_0123456789abcdef0123456789abcdef";
    const markup = renderToStaticMarkup(<RecordSnippet label="任务 ID" value={hash} />);
    expect(markup).toContain("task_012345678…cdef");
    expect(markup).toContain(hash);
    expect(markup).toContain("复制任务 ID");
  });
});
