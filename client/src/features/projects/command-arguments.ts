import { z } from "zod";

const bytes = new TextEncoder();
export const commandArguments = z
  .array(z.string().refine((arg) => !arg.includes("\0") && bytes.encode(arg).length <= 16384))
  .min(1)
  .max(128)
  .refine((argv) => argv[0] !== "");

// 只展开脚本位置明确的解释器，位置参数与执行选项随原配置保存。
export function shellScriptIndex(argv: string[]): number | undefined {
  if (!/^(?:.*\/)?(?:sh|bash|dash|ash|ksh|zsh)$/.test(argv[0] ?? "")) return undefined;
  for (let index = 1; index < argv.length - 1; index++) {
    const flag = argv[index];
    if (["--noprofile", "--norc", "--login", "--posix", "--restricted"].includes(flag)) continue;
    if (flag === "--rcfile" || flag === "--init-file" || /^[+-][oO]$/.test(flag)) {
      index++;
      continue;
    }
    if (!/^-[a-zA-Z]+$/.test(flag) || /[oO]/.test(flag)) break;
    if (flag.includes("c")) return index + 1;
  }
  return undefined;
}

const safeWord = /^[\w./:@%=+,-]+$/;
const assignment = /^[A-Za-z_][A-Za-z0-9_]*=/;
const shellKeyword =
  /^(?:!|if|then|else|elif|fi|for|in|while|until|do|done|case|esac|function|select|time|exec|export|unset|set|cd|read|eval|source|trap|return|exit|shift|wait|umask|ulimit|alias|unalias|break|continue|readonly|local|command|type|hash|\.|:)$/;

function quotedWord(word: string, index: number) {
  if (safeWord.test(word) && !(index === 0 && (shellKeyword.test(word) || assignment.test(word))))
    return word;
  return `'${word.replaceAll("'", "'\\''")}'`;
}

export const commandDraftSchema = z.object({
  text: z.string(),
  original: z.array(z.string()),
  shellEntrypoint: z.boolean(),
});
type CommandDraft = z.infer<typeof commandDraftSchema>;

export function commandDraft(original: string[] = [], shellEntrypoint = false): CommandDraft {
  const script = shellScriptIndex(original);
  return {
    original,
    text: script === undefined ? original.map(quotedWord).join(" ") : original[script],
    shellEntrypoint,
  };
}

// 只解码可静态还原的词。涉及展开、控制流或重定向时将整段原文交给容器的解释器。
function literalArguments(text: string): string[] | undefined {
  const tokens = /[ \t]+|[\w./:@%=+,-]+|'[^']*'|"(?:[^"\\$\x60]|\\[\s\S])*"|\\[^\n]/gy;
  const words: string[] = [];
  let word: string | undefined;
  let keyword = false;
  while (tokens.lastIndex < text.length) {
    const token = tokens.exec(text)?.[0];
    if (!token) return undefined;
    if (/^[ \t]/.test(token)) {
      if (word !== undefined) words.push(word);
      word = undefined;
      continue;
    }
    const bare = safeWord.test(token);
    if (!words.length && word === undefined && bare && assignment.test(token)) return undefined;
    if (words.length === 0 && word === undefined) keyword = bare && shellKeyword.test(token);
    const value = bare
      ? token
      : token.startsWith("'")
        ? token.slice(1, -1)
        : token.startsWith('"')
          ? token
              .slice(1, -1)
              .replace(/\\(["\\$\x60\n])/g, (_, ch: string) => (ch === "\n" ? "" : ch))
          : token.slice(1);
    word = (word ?? "") + value;
  }
  if (word !== undefined) words.push(word);
  return keyword && shellKeyword.test(words[0]) ? undefined : words;
}

export function commandValue(draft: CommandDraft): { argv: string[]; shellEntrypoint?: true } {
  const script = shellScriptIndex(draft.original);
  if (draft.text === commandDraft(draft.original).text) {
    return draft.shellEntrypoint
      ? { argv: draft.original, shellEntrypoint: true }
      : { argv: draft.original };
  }
  if (!draft.text.trim()) return { argv: [] };
  if (script !== undefined && !draft.shellEntrypoint) {
    return { argv: draft.original.map((arg, index) => (index === script ? draft.text : arg)) };
  }
  const direct = literalArguments(draft.text);
  return direct ? { argv: direct } : { argv: ["/bin/sh", "-c", draft.text], shellEntrypoint: true };
}
