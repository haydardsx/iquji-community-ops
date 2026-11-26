import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { JsonInspector } from "./json-inspector";

describe("JsonInspector", () => {
  it("只读 JSON 查看器不渲染格式化按钮与多余工具栏", () => {
    const markup = renderToStaticMarkup(
      <JsonInspector label="构建详情" value={{ status: "succeeded", id: "build_example" }} />,
    );
    expect(markup).toContain("ops-json-inspector");
    expect(markup).not.toContain("格式化");
    expect(markup).not.toContain("iquji-editor-toolbar");
  });
});
