import { createTranslator, parseLocale } from "@iquji-community/web-i18n";
import { describe, expect, it } from "vitest";
import { opsCatalogs } from "./index";

describe("OPS 国际化与双语词典", () => {
  it("应包含完整的中英文词典配置", () => {
    expect(opsCatalogs["zh-CN"]).toBeDefined();
    expect(opsCatalogs["en-US"]).toBeDefined();
  });

  it("zh-CN 与 en-US 词条应 100% 对齐且非空", () => {
    const zhKeys = Object.keys(opsCatalogs["zh-CN"]);
    const enKeys = Object.keys(opsCatalogs["en-US"]);

    expect(zhKeys.length).toBeGreaterThan(0);
    expect(enKeys.length).toBe(zhKeys.length);

    for (const key of zhKeys) {
      const zhVal = opsCatalogs["zh-CN"][key as keyof (typeof opsCatalogs)["zh-CN"]];
      const enVal = opsCatalogs["en-US"][key as keyof (typeof opsCatalogs)["en-US"]];

      expect(zhVal, `zh-CN key [${key}] should not be empty`).toBeTruthy();
      expect(enVal, `en-US key [${key}] should not be empty`).toBeTruthy();
    }
  });

  it("应正确插值动态参数", () => {
    const zhTranslate = createTranslator(opsCatalogs["zh-CN"]);
    const enTranslate = createTranslator(opsCatalogs["en-US"]);

    const zhMsg = zhTranslate("projects.deleteDialogDesc", { name: "示例项目" });
    expect(zhMsg).toContain("“示例项目”");

    const enMsg = enTranslate("projects.deleteDialogDesc", { name: "DemoProject" });
    expect(enMsg).toContain("“DemoProject”");
  });

  it("应正确解析语言编码", () => {
    expect(parseLocale("zh-CN")).toBe("zh-CN");
    expect(parseLocale("zh")).toBe("zh-CN");
    expect(parseLocale("en-US")).toBe("en-US");
    expect(parseLocale("en")).toBe("en-US");
    expect(parseLocale("unknown")).toBeUndefined();
  });
});
