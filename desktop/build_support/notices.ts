import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { licenseTexts, NoticeIndex } from "../../client/scripts/notices";

const desktop = path.resolve(import.meta.dirname, "..");
const root = path.dirname(desktop);
const supplemental = (name: string) =>
  fs.readFileSync(path.join(desktop, "license-sources", name), "utf8").trim();
const index = new NoticeIndex();

function command(program: string, args: string[], cwd = desktop, env = process.env) {
  return execFileSync(program, args, {
    cwd,
    env,
    encoding: "utf8",
    maxBuffer: 32 * 1024 * 1024,
  }).trim();
}

type GoModule = { Path: string; Version: string; Sum?: string; Replace?: GoModule };
function goNotices() {
  const packages = new Map<string, GoModule>();
  const toolchains = new Set<string>();
  const agent = path.join(root, "agent/dist");
  const runtime = path.join(desktop, "runtime");
  const suffix = process.env.TAURI_ENV_PLATFORM === "windows" ? ".exe" : "";
  const binaries = [
    ...["frpc", "iquops-server", "iquops-worker", "iquops-mcp"].map((name) => path.join(runtime, name + suffix)),
    ...["amd64", "arm64"].map((arch) => path.join(agent, "iquops-monitoring-agent-linux-" + arch)),
  ];
  const go = (args: string[], env = process.env) =>
    command("go", args, path.join(root, "server"), env);
  for (const file of binaries) {
    const build = JSON.parse(go(["version", "-m", "-json", file]));
    toolchains.add(build.GoVersion);
    for (const mod of [build.Main, ...(build.Deps ?? [])] as GoModule[]) {
      const item = mod.Replace ?? mod;
      if (item.Version === "(devel)") continue;
      if (!item.Path || !item.Version) throw new Error("Go 模块缺少版本");
      packages.set(item.Path + "@" + item.Version, item);
    }
  }
  for (const version of toolchains) {
    if (!/^go\d+\.\d+(?:\.\d+)?$/.test(version)) throw new Error("Go 工具链版本无效");
    const env = { ...process.env, GOTOOLCHAIN: version };
    const toolchains = process.env.IQUJI_GO_TOOLCHAINS_DIR;
    const local = toolchains && path.join(toolchains, version);
    if (local && fs.existsSync(path.join(local, "bin/go"))) env.GOROOT = local;
    const goroot = command("go", ["env", "GOROOT"], desktop, env);
    const notices = licenseTexts(goroot);
    if (!notices.some((text) => text.includes("Redistribution and use"))) {
      notices.push(supplemental("go-LICENSE.txt"));
    }
    index.add("go:runtime@" + version, notices);
  }
  for (const [id, mod] of packages) {
    const data = JSON.parse(go(["mod", "download", "-json", id]));
    if (!data.Dir || (mod.Sum && data.Sum !== mod.Sum)) throw new Error("Go 模块校验失败：" + id);
    index.add("go:" + id, licenseTexts(data.Dir));
  }
}

type Crate = {
  name: string;
  version: string;
  authors: string[];
  source?: string;
  repository?: string;
  manifest_path: string;
  license_file?: string;
};
function rustNotices() {
  const rust = command("rustc", ["-vV"]);
  const host = /^host: (.+)$/m.exec(rust)?.[1];
  if (!host) throw new Error("无法确定 Rust 目标");
  const target = process.env.IQUOPS_DESKTOP_TARGET || process.env.CARGO_BUILD_TARGET || host;
  const targets =
    target === "universal-apple-darwin"
      ? ["aarch64-apple-darwin", "x86_64-apple-darwin"]
      : [target];
  const packages = new Map<string, Crate>();
  for (const triple of targets) {
    const manifest = ["--locked", "--manifest-path", path.join(desktop, "Cargo.toml")];
    const tree = command("cargo", [
      "tree",
      ...manifest,
      "--target",
      triple,
      "--edges",
      "normal,no-proc-macro",
      "--prefix",
      "none",
      "--format",
      "{p}",
    ]);
    const linked = new Set(tree.split(/\r?\n/).map((line) => line.split(" (")[0]));
    const meta = JSON.parse(
      command("cargo", [
        "metadata",
        ...manifest,
        "--format-version",
        "1",
        "--filter-platform",
        triple,
      ]),
    );
    for (const p of meta.packages as Crate[]) {
      if (p.source && linked.has(p.name + " v" + p.version))
        packages.set(p.name + "@" + p.version, p);
    }
  }
  if (!packages.size) throw new Error("未识别到 Rust 依赖");
  const libVersion = /^release: (.+)$/m.exec(rust)?.[1];
  const sysroot = command("rustc", ["--print", "sysroot"]);
  const doc = ["share/doc/rust", "share/doc/rustc"]
    .map((d) => path.join(sysroot, d, "COPYRIGHT-library.html"))
    .find((p) => fs.existsSync(p));
  if (!doc) throw new Error("Rust 工具链缺少标准库版权声明；请安装 rust-docs");
  index.add("rust:std@" + libVersion, [fs.readFileSync(doc, "utf8"), supplemental("rust-MIT.txt")]);
  for (const [id, p] of packages) {
    const directory = path.dirname(p.manifest_path);
    const notices = licenseTexts(directory);
    if (p.license_file)
      notices.push(fs.readFileSync(path.resolve(directory, p.license_file), "utf8").trim());
    if (!notices.length) {
      if (id === "alloc-stdlib@0.2.4") notices.push(supplemental("alloc-stdlib-BSD.txt"));
      if (id === "dlopen2@0.8.2") notices.push(supplemental("dlopen2-MIT.txt"));
      if (p.repository === "https://github.com/madsmtm/objc2") {
        notices.push(
          "Authors: " + p.authors.join(", "),
          supplemental("objc2-LICENSE.md"),
          supplemental("unic-MIT.txt"),
        );
      }
      if (p.repository?.replace(/\/$/, "") === "https://github.com/open-i18n/rust-unic") {
        notices.push(
          ...["unic-COPYRIGHT.md", "unic-MIT.txt", "unic-AUTHORS.txt"].map(supplemental),
        );
      }
    }
    if (p.name === "aws-lc-sys") {
      notices.push(
        ...licenseTexts(path.join(directory, "aws-lc")),
        ...licenseTexts(path.join(directory, "aws-lc/third_party/fiat")),
      );
    }
    index.add("cargo:" + id, notices);
  }
}

function checkRuntimeResources() {
  const runtime = path.join(desktop, "runtime");
  const suffix = process.env.TAURI_ENV_PLATFORM === "windows" ? ".exe" : "";
  const files = new Set([
    "frpc" + suffix, "iquops-server" + suffix, "iquops-worker" + suffix, "iquops-mcp" + suffix,
    "frpc-LICENSE", "third-party-notices.json", "jenkins-plugins.tar",
  ]);
  for (const entry of fs.readdirSync(runtime, { withFileTypes: true })) {
    if (!entry.isFile() || !files.has(entry.name)) throw new Error("桌面运行时包含未声明资源");
  }
}

function jenkinsNotices() {
  type Notice = { files: string[]; source: string; copyright?: string };
  const catalog: Record<string, Notice> = JSON.parse(supplemental("jenkins/NOTICE.json"));
  const lock = fs.readFileSync(path.join(root, "server/resources/runtime/managed/jenkins/plugins.lock"), "utf8");
  for (const entry of lock.trim().split(/\r?\n/)) {
    const [id, version] = entry.split(":");
    const component = id + "@" + version;
    const notice = catalog[component];
    if (!notice?.files.length) throw new Error("缺少 Jenkins 插件许可：" + component);
    const texts = notice.files.map((name) => supplemental(path.join("jenkins", name)));
    texts.push("源码与许可声明：" + notice.source);
    if (notice.copyright) texts.push(notice.copyright);
    index.add("jenkins:" + component, texts);
  }
}

checkRuntimeResources();
goNotices();
rustNotices();
jenkinsNotices();
fs.writeFileSync(path.join(desktop, "runtime/third-party-notices.json"), index.serialize());
