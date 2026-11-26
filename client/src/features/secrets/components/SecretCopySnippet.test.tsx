import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { SecretCopySnippet } from "./SecretCopySnippet";

describe("SecretCopySnippet 凭据属性复制条", () => {
  it("正常渲染数值与代码块", () => {
    const markup = renderToStaticMarkup(
      <SecretCopySnippet label="Secret Ref" value="secret_ref_123456" />,
    );
    expect(markup).toContain("secret_ref_123456");
    expect(markup).toContain("ops-secret-snippet");
    expect(markup).toContain("ops-secret-snippet__copy-btn");
  });

  it.each([undefined, "脱敏摘要"])("正文和悬浮提示均使用脱敏文本（标签 %s）", (label) => {
    const syntheticValue = "sample-value-for-copy";
    const maskedValue = "**** 0123";
    const markup = renderToStaticMarkup(
      <SecretCopySnippet displayValue={maskedValue} label={label} value={syntheticValue} />,
    );
    expect(markup).toContain(maskedValue);
    expect(markup).toContain(`title="${label ? `${label}：` : ""}${maskedValue}"`);
    expect(markup).not.toContain(syntheticValue);
  });

  it("空值显示占位符并隐藏复制按钮", () => {
    const markup = renderToStaticMarkup(<SecretCopySnippet value="" />);
    expect(markup).toContain("—");
    expect(markup).not.toContain("ops-secret-snippet__copy-btn");
  });
});
