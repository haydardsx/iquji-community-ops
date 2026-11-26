import { renderToStaticMarkup as renderWithoutProvider } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "@/test/render";
import { useI18n } from "./index";

function LanguageProbe() {
  const { locale, t } = useI18n();
  return <span lang={locale}>{t("common.save")}</span>;
}

describe("应用语言上下文", () => {
  it("缺少正式 Provider 时明确失败，不静默退回中文", () => {
    expect(() => renderWithoutProvider(<LanguageProbe />)).toThrow("IqujiLocaleProvider");
  });

  it("静态界面测试使用正式 Provider，并能切换测试语言", () => {
    expect(renderToStaticMarkup(<LanguageProbe />)).toContain('lang="zh-CN">保存');
    expect(renderToStaticMarkup(<LanguageProbe />, "en-US")).toContain('lang="en-US">Save');
  });
});
