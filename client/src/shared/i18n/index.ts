import {
  IQUJI_LOCALES,
  IqujiLocaleProvider,
  type Locale,
  type MessageCatalog,
  type MessageValues,
  useIqujiLocale,
} from "@iquji-community/web-i18n";
import { enUS } from "./locales/en-US";
import { type OpsMessageKey, zhCN } from "./locales/zh-CN";

export const opsCatalogs: Readonly<Record<Locale, MessageCatalog<OpsMessageKey>>> = {
  "zh-CN": zhCN,
  "en-US": enUS,
};

export type { OpsMessageKey };

export type TranslateFn = {
  (key: OpsMessageKey, values?: MessageValues): string;
  (key: string, values?: any): string;
};

export function useI18n() {
  const { locale, setLocale, translate } = useIqujiLocale<OpsMessageKey>();
  return { locale, setLocale, t: translate as TranslateFn };
}

export { IQUJI_LOCALES, IqujiLocaleProvider, type Locale };
