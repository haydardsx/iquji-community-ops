import type { ReactNode } from "react";
import { renderToStaticMarkup as renderMarkup } from "react-dom/server";
import { IqujiLocaleProvider, type Locale, opsCatalogs } from "@/shared/i18n";

/** 静态界面测试使用正式词典上下文，与应用的语言配置保持一致。 */
export function renderToStaticMarkup(children: ReactNode, locale: Locale = "zh-CN") {
  return renderMarkup(
    <IqujiLocaleProvider catalogs={opsCatalogs} defaultLocale={locale}>
      {children}
    </IqujiLocaleProvider>,
  );
}
