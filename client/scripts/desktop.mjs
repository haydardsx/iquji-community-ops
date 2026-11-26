#!/usr/bin/env bun
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, realpathSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import path from "node:path";

const communityRoot = path.resolve(import.meta.dirname, "../..");
const desktopRoot = path.join(communityRoot, "desktop");
const cli = path.join(communityRoot, "client/node_modules/@tauri-apps/cli/tauri.js");
const separator = "\u001f";

function pathForms(directory, base = process.cwd()) {
  const absolute = path.resolve(base, directory);
  try {
    const resolved = realpathSync(absolute);
    const forms = [absolute, resolved];
    // macOS 在程序参数与文件系统 API 中可分别使用 /var 或 /private/var。
    if (process.platform === "darwin" && /^\/private\/(?:var|tmp)(?:\/|$)/.test(resolved)) {
      forms.push(resolved.slice("/private".length));
    }
    return [...new Set(forms)];
  } catch {
    return [absolute];
  }
}

export function desktopEnvironment(command, environment = process.env) {
  if (command !== "build") return environment;

  // Cargo 的编码参数优先于 RUSTFLAGS；未设置编码参数时，按 Cargo 的空白分词规则保留原参数。
  const previous =
    environment.CARGO_ENCODED_RUSTFLAGS !== undefined
      ? environment.CARGO_ENCODED_RUSTFLAGS
      : (environment.RUSTFLAGS ?? "").trim().split(/\s+/u).filter(Boolean).join(separator);
  const home = environment.HOME || environment.USERPROFILE || homedir();
  const temporary = environment.TMPDIR || environment.TEMP || environment.TMP || tmpdir();
  const mappings = [
    [home, "<home>"],
    [temporary, "<tmp>"],
    ...(environment.USERPROFILE ? [[environment.USERPROFILE, "<home>"]] : []),
    ...(environment.RUNNER_TEMP ? [[environment.RUNNER_TEMP, "<tmp>"]] : []),
    [environment.CARGO_HOME || path.join(home, ".cargo"), "<cargo>"],
    [environment.RUSTUP_HOME || path.join(home, ".rustup"), "<rustup>"],
    [environment.CARGO_TARGET_DIR || path.join(desktopRoot, "target"), "<target>"],
    [communityRoot, "<community>"],
  ];
  // rustc 使用最后一个匹配项，让较具体的目录覆盖其父目录。
  const prefixes = new Map(
    mappings.flatMap(([source, target]) =>
      pathForms(source, desktopRoot).map((prefix) => [prefix, target]),
    ),
  );
  const flags = [...prefixes.entries()]
    .sort(([left], [right]) => left.length - right.length)
    .map(([prefix, target]) => `--remap-path-prefix=${prefix}=${target}`);
  const native = {};
  if (process.platform === "win32") {
    // MSVC 链接器默认会把 PDB 的绝对路径写入 PE 的 CodeView 记录。
    flags.push("-Clink-arg=/PDBALTPATH:%_PDB%", "-Clink-arg=/Brepro");
    // AWS-LC 的 cl 模式不自动映射 __FILE__；clang-cl 能在编译 C 源码时完成映射。
    native.AWS_LC_SYS_CC = "clang-cl";
    native.CC_SHELL_ESCAPED_FLAGS = "1";
    const mapped = [...prefixes.entries()].flatMap(([prefix, target]) =>
      [...new Set([prefix, prefix.replaceAll("\\", "/")])].map(
        (source) => `'/clang:-ffile-prefix-map=${source}=${target}'`,
      ),
    );
    native.AWS_LC_SYS_CFLAGS = [environment.AWS_LC_SYS_CFLAGS, ...mapped].filter(Boolean).join(" ");
  }
  return {
    ...environment,
    ...native,
    CARGO_ENCODED_RUSTFLAGS: [previous, ...flags].filter(Boolean).join(separator),
  };
}

export function runDesktop(args = process.argv.slice(2)) {
  const [command, ...options] = args;
  const env = desktopEnvironment(command);
  const namedTarget = options.find((value) => value.startsWith("--target="));
  const targetAt = options.findIndex((value) => value === "--target" || value === "-t");
  if (command === "build" && (namedTarget || targetAt >= 0)) {
    env.IQUOPS_DESKTOP_TARGET = namedTarget
      ? namedTarget.slice("--target=".length)
      : options[targetAt + 1];
  }
  const defaults = [];
  if (
    command === "build" &&
    process.platform === "darwin" &&
    !process.env.APPLE_SIGNING_IDENTITY &&
    !process.env.APPLE_CERTIFICATE &&
    !options.includes("--no-sign") &&
    !options.includes("--no-bundle")
  ) {
    const hasSigningIdentity = ["tauri.macos.conf.json", "tauri.conf.json"].some((name) => {
      const file = path.join(desktopRoot, name);
      return (
        existsSync(file) && JSON.parse(readFileSync(file, "utf8")).bundle?.macOS?.signingIdentity
      );
    });
    // Tauri 按顺序合并配置，调用者传入的签名设置优先于本地默认值。
    if (!hasSigningIdentity)
      defaults.push("--config", '{"bundle":{"macOS":{"signingIdentity":"-"}}}');
  }
  const cliArgs = command ? [command, ...defaults, ...options] : [];
  const result = spawnSync("bun", [cli, ...cliArgs], {
    cwd: desktopRoot,
    stdio: "inherit",
    env,
  });
  if (result.error) throw result.error;
  process.exit(result.status ?? 1);
}

if (import.meta.main) runDesktop();
