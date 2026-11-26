import type { TranslateFn } from "@/shared/i18n";
import { limits } from "../../../../server/internal/automation/commands.json";

export type Mode = "guide" | "direct";
export type Level = Mode | "disabled" | "mixed";
// 未写入 mode 的旧记录沿用既有直接权限，下一次保存时写入显式模式。
export type Permission = { command: string; mode?: Mode; targets: string[] };
export type Rule = { command: string; target: string };
export type Client = { id: string; name: string; permissions: Permission[]; rules: Rule[] };
export type Command = {
  group: string;
  name: string;
  description: string;
  fields: string[] | null;
  required: string[] | null;
  confirm: boolean;
  guide?: boolean;
};
export type Summary = { clients: Client[]; commands: Command[]; version: number };
export type Scope = { limited: boolean; targets: string; exceptions: string };
export type ActionScopes = Partial<Record<Mode, Scope>>;
export type PolicyDraft = Record<string, ActionScopes>;
export const emptyPermission = { limited: false, targets: "", exceptions: "" };

export function policyDraft(client?: Client): PolicyDraft {
  if (!client) return { command_catalog: { direct: { ...emptyPermission } } };
  const draft: PolicyDraft = {};
  for (const { command, mode = "direct", targets } of client.permissions ?? []) {
    if (command === "action_guide") continue;
    draft[command] ??= {};
    draft[command][mode] = {
      limited: Boolean(targets?.length),
      targets: (targets ?? []).join("\n"),
      exceptions:
        mode === "direct"
          ? (client.rules ?? [])
              .filter((r) => r.command === command)
              .map((r) => r.target)
              .join("\n")
          : "",
    };
  }
  return draft;
}

export function permissionLevel(scopes?: ActionScopes): Level {
  if (scopes?.guide && scopes.direct) return "mixed";
  if (scopes?.guide) return "guide";
  return scopes?.direct ? "direct" : "disabled";
}

export function changePermissionLevel(
  draft: PolicyDraft,
  command: string,
  level: Level,
): PolicyDraft {
  const next = { ...draft };
  if (level === "disabled") {
    delete next[command];
    return next;
  }
  const scopes = draft[command] ?? {};
  if (level === "mixed") {
    // 第二种模式从待填写的精确范围开始，避免编辑时扩大现有授权。
    const exact = { ...emptyPermission, limited: true };
    next[command] = { guide: scopes.guide ?? { ...exact }, direct: scopes.direct ?? { ...exact } };
  } else {
    const other = scopes[level === "guide" ? "direct" : "guide"];
    next[command] = { [level]: scopes[level] ?? { ...(other ?? emptyPermission), exceptions: "" } };
  }
  return next;
}

export function targetLines(raw: string): string[] {
  return [
    ...new Set(
      raw
        .split("\n")
        .map((v) => v.trim())
        .filter(Boolean),
    ),
  ];
}

export function overlappingTargets(scopes?: ActionScopes): "all" | string[] {
  if (!scopes?.direct || !scopes.guide) return [];
  if (!scopes.direct.limited) return "all";
  const direct = targetLines(scopes.direct.targets);
  if (!scopes.guide.limited) return direct;
  const guide = new Set(targetLines(scopes.guide.targets));
  return direct.filter((target) => guide.has(target));
}

function validateScope(mode: Mode, scope: Scope): boolean {
  const encoder = new TextEncoder();
  const targets = scope.limited ? targetLines(scope.targets) : [];
  const exceptions = mode === "direct" ? targetLines(scope.exceptions) : [];
  if (
    scope.limited &&
    (!targets.length || exceptions.some((target) => !targets.includes(target)))
  ) {
    return false;
  }
  if (targets.length > limits.maxTargetsPerCommand) return false;
  return ![...targets, ...exceptions].some(
    (v) => v.includes("*") || /\p{Cc}/u.test(v) || encoder.encode(v).length > limits.maxTargetBytes,
  );
}

export function isActionScopeValid(scopes?: ActionScopes): boolean {
  if (!scopes) return true;
  for (const mode of ["guide", "direct"] as const) {
    const scope = scopes[mode];
    if (scope && !validateScope(mode, scope)) return false;
  }
  return true;
}

export function policyPayload(draft: PolicyDraft) {
  const permissions: (Permission & { mode: Mode })[] = [];
  const rules: Rule[] = [];
  let valid = true;
  for (const [command, scopes] of Object.entries(draft)) {
    if (command === "action_guide") continue;
    for (const mode of ["guide", "direct"] as const) {
      const scope = scopes[mode];
      if (!scope) continue;
      if (!validateScope(mode, scope)) valid = false;
      const targets = scope.limited ? targetLines(scope.targets) : [];
      const exceptions = mode === "direct" ? targetLines(scope.exceptions) : [];
      permissions.push({ command, mode, targets });
      for (const target of exceptions) rules.push({ command, target });
    }
  }
  return { permissions, rules, valid: valid && rules.length <= limits.maxPreapprovals };
}

export function summarizeDraft(draft: PolicyDraft, commands: Command[]) {
  const editable = commands.filter((cmd) => cmd.name !== "action_guide");
  let granted = 0;
  let direct = 0;
  let guide = 0;
  for (const cmd of editable) {
    const scopes = draft[cmd.name];
    const level = permissionLevel(scopes);
    if (level === "disabled") continue;
    granted++;
    if (scopes?.direct) direct++;
    if (scopes?.guide) guide++;
  }
  return { granted, direct, guide, total: editable.length };
}

export function describeScopeSummary(
  scopes: ActionScopes | undefined,
  command: Command,
  t: TranslateFn,
): { label: string; invalid: boolean } {
  if (!scopes) return { label: t("automation.scopeAllSummary"), invalid: false };
  const invalid = !isActionScopeValid(scopes);
  if (invalid) return { label: t("automation.scopeNeedTargets"), invalid: true };

  const activeScopes = (["guide", "direct"] as const)
    .map((mode) => scopes[mode])
    .filter((s): s is Scope => Boolean(s));
  if (activeScopes.length === 0) return { label: t("automation.scopeAllSummary"), invalid: false };

  const allUnlimited = activeScopes.every((s) => !s.limited);
  const directExceptions =
    command.confirm && scopes.direct ? targetLines(scopes.direct.exceptions).length : 0;

  let base: string;
  if (allUnlimited) {
    base = t("automation.scopeAllSummary");
  } else {
    const totalTargets = activeScopes.reduce(
      (sum, s) => sum + (s.limited ? targetLines(s.targets).length : 0),
      0,
    );
    base = t("automation.scopeLimitedSummary", { count: totalTargets });
  }

  if (directExceptions > 0) {
    return {
      label: `${base} · ${t("automation.scopePreapprovedSummary", { count: directExceptions })}`,
      invalid: false,
    };
  }
  return { label: base, invalid: false };
}
