import { IqujiBadge } from "@iquji-community/web-component";
import type { Application } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";

export function ApplicationInputStatus({
  status,
  changes = [],
  detail = false,
}: {
  status?: Application["inputStatus"];
  detail?: boolean;
  changes?: Application["inputChanges"];
}) {
  const { locale } = useI18n();
  if (!status) return null;
  const messages =
    locale === "en-US"
      ? {
          restart: "Inputs changed · restart required",
          rebuild: "Build inputs changed · rebuild required",
          unavailable: "Referenced output unavailable",
        }
      : {
          restart: "运行变量有变更，待重启",
          rebuild: "构建变量有变更，待重新构建",
          unavailable: "引用的输出暂不可用",
        };
  return (
    <span className="ops-row" title={messages[status]}>
      <IqujiBadge variant="dot" tone="danger" aria-label={messages[status]} />
      {detail && (
        <span>
          {messages[status]}
          {changes.length > 0 && (
            <span className="ops-input-changes">
              {changes.map((c) => {
                const stage =
                  c.stage === "build"
                    ? locale === "en-US"
                      ? "Build"
                      : "构建期"
                    : locale === "en-US"
                      ? "Runtime"
                      : "运行期";
                const source =
                  c.source === "removed"
                    ? locale === "en-US"
                      ? "Removed"
                      : "已移除"
                    : [c.applicationName, c.outputName, c.credentialName]
                        .filter(Boolean)
                        .join(" / ");
                return (
                  <span key={JSON.stringify([c.stage, c.serviceSlotKey, c.key])}>
                    {[stage, c.serviceSlotKey, c.key, source].filter(Boolean).join(" · ")}
                  </span>
                );
              })}
            </span>
          )}
        </span>
      )}
    </span>
  );
}
