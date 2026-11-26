import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";
import { diagnostic } from "./diagnostic.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const sourceRoot = path.join(root, "client/src");
const errors = [];
const sourceExtensions = new Set([
  ".css",
  ".go",
  ".groovy",
  ".html",
  ".js",
  ".jsx",
  ".json",
  ".mjs",
  ".rs",
  ".sh",
  ".sql",
  ".toml",
  ".ts",
  ".tsx",
  ".vue",
  ".yaml",
  ".yml",
]);
const forbiddenImports = [
  "@base-ui/react",
  "lucide-react",
  "ky",
  "@sentry/react",
  "@pandacss",
  "styled-system",
  "@tanstack/react-table",
];
const nativeTags = new Set(["button", "select", "input", "textarea"]);
const sharedExports = new Set();
const clientRoot = path.join(root, "client");
const clientManifest = JSON.parse(fs.readFileSync(path.join(clientRoot, "package.json"), "utf8"));
for (const name of Object.keys(clientManifest.dependencies).filter((name) =>
  name.startsWith("@iquji-community/"),
)) {
  const manifest = path.join(clientRoot, "node_modules", name, "package.json");
  const metadata = JSON.parse(fs.readFileSync(manifest, "utf8"));
  for (const key of Object.keys(metadata.exports || {})) {
    if (key === ".") sharedExports.add(metadata.name);
    else if (key.startsWith("./")) sharedExports.add(metadata.name + key.slice(1));
  }
}

// 同时核对已跟踪文件与尚未暂存的候选文件。
const tracked = spawnSync(
  "git",
  ["-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "."],
  { encoding: "utf8" },
);
if (tracked.error || tracked.status !== 0) throw new Error("无法读取 Community 提交文件清单");
const trackedNames = tracked.stdout.split("\0").filter(Boolean);
for (const name of trackedNames) {
  const file = path.join(root, name);
  if (!fs.existsSync(file)) continue;
  const stat = fs.lstatSync(file);
  if (stat.isSymbolicLink()) {
    errors.push(`${name}: 不得提交符号链接`);
    continue;
  }
  if (!stat.isFile()) continue;
  if (/(?:^|\/)(?:node_modules|target|dist|\.cache|\.local)(?:\/|$)/u.test(name)) {
    errors.push(`${name}: 不得提交缓存或生成物`);
  }
  if (
    /(?:^|\/)(?:\.env(?:\..*)?|credentials(?:\..*)?|id_(?:rsa|ed25519)|.*\.(?:pem|key|p12|pfx|sqlite|db|log))$/u.test(
      name,
    ) &&
    !name.endsWith(".env.example")
  ) {
    errors.push(`${name}: 不得提交本机状态或密钥材料`);
  }
  if (stat.size > 10 * 1024 * 1024) errors.push(`${name}: 文件超过 10 MiB`);
  const bytes = fs.readFileSync(file);
  if (bytes.includes(0)) continue;
  const content = bytes.toString("utf8");
  if (
    /(?:github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})|^\s*-{5}BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-{5}\s*$/mu.test(
      content,
    )
  ) {
    errors.push(`${name}: 疑似秘密材料，请在本地检查`);
  }
  // 固定镜像中使用 BuildKit 的默认配置位置。
  const pathContent =
    name === "server/resources/runtime/managed/jenkins/broker-isolation.mjs"
      ? content.replaceAll("/home/user/.config/buildkit/buildkitd.toml", "<container-config>")
      : content;
  if (
    name !== "client/scripts/check-boundaries.mjs" &&
    /(?:\/private\/tmp\/|\/Users\/[A-Za-z0-9_.-]+\/|\/home\/[A-Za-z0-9_.-]+\/|[A-Za-z]:\\Users\\[^\\]+\\)/u.test(
      pathContent,
    )
  ) {
    errors.push(`${name}: 引用了本机绝对路径`);
  }
  const privateImplementation = /(?:^|[/"'`])(?:enterprise|internal-only)(?:\/|["'`])/mu;
  if (privateImplementation.test(content)) {
    errors.push(`${name}: 引用了不可分发的模块`);
  }
  if (sourceExtensions.has(path.extname(name)) && content.trimEnd().split(/\r?\n/u).length > 600) {
    errors.push(`${name}: 源码超过 600 行`);
  }
}

for (const file of walk(sourceRoot)) {
  const relative = path.relative(root, file);
  const source = ts.createSourceFile(
    relative,
    fs.readFileSync(file, "utf8"),
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const report = (node, message) => {
    errors.push(
      `${relative}:${source.getLineAndCharacterOfPosition(node.getStart()).line + 1}: ${message}`,
    );
  };
  const checkImport = (node, name) => {
    if (forbiddenImports.some((prefix) => name === prefix || name.startsWith(`${prefix}/`))) {
      report(node, "请从 @iquji-community 的 Web 包导入相应组件");
    }
    if (
      (name.startsWith("@iquji-community/") && !sharedExports.has(name)) ||
      /shared\/web\//u.test(name)
    ) {
      report(node, "请使用已发布 Web 包的导出入口");
    }
  };
  const visit = (node) => {
    if (
      (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) &&
      node.moduleSpecifier &&
      ts.isStringLiteral(node.moduleSpecifier)
    ) {
      checkImport(node, node.moduleSpecifier.text);
    }
    if (
      ts.isCallExpression(node) &&
      node.expression.kind === ts.SyntaxKind.ImportKeyword &&
      node.arguments[0] &&
      ts.isStringLiteral(node.arguments[0])
    ) {
      checkImport(node, node.arguments[0].text);
    }
    if (
      (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) &&
      ts.isIdentifier(node.tagName) &&
      nativeTags.has(node.tagName.text)
    ) {
      report(node, `请使用 @iquji-community/web-component 提供的 ${node.tagName.text} 控件`);
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
}

if (errors.length > 0) {
  if (process.argv.includes("--locations") && !process.env.CI) {
    for (const error of new Set(errors))
      diagnostic("error", "community_check_failed", error, "COMMUNITY_BOUNDARY_FAILED");
  } else {
    diagnostic(
      "error",
      "community_check_failed",
      "项目文件或组件依赖检查失败",
      "COMMUNITY_BOUNDARY_FAILED",
    );
  }
  process.exit(1);
}
diagnostic("info", "community_check_passed", "Community 内容与 UI 边界检查通过");

function* walk(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) yield* walk(absolute);
    else if (/\.(ts|tsx)$/u.test(entry.name)) yield absolute;
  }
}
