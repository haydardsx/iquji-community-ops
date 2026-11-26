import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import type { Plugin } from "vite";
import { licenseTexts, NoticeIndex } from "./notices";

function modulePackage(id: string) {
  if (/^\0(?:vite\/|commonjsHelpers\.js$)/.test(id)) {
    return path.dirname(createRequire(import.meta.url).resolve("vite/package.json"));
  }
  const file = id.replace(/^\0/, "").split("?")[0];
  if (!file.includes(`${path.sep}node_modules${path.sep}`) || !fs.existsSync(file)) return;
  const resolved = fs.realpathSync(file);
  const marker = `${path.sep}node_modules${path.sep}`;
  const boundary = resolved.lastIndexOf(marker);
  if (boundary < 0) throw new Error("依赖实际路径不在已安装包内");
  const start = boundary + marker.length;
  const parts = resolved.slice(start).split(path.sep);
  const name = parts.slice(0, parts[0].startsWith("@") ? 2 : 1);
  return path.join(resolved.slice(0, start), ...name);
}

export function webNotices(): Plugin {
  return {
    name: "third-party-notices",
    apply: "build",
    generateBundle(_options, bundle) {
      const roots = new Set<string>();
      for (const chunk of Object.values(bundle)) {
        if (chunk.type !== "chunk") continue;
        for (const [id, info] of Object.entries(chunk.modules)) {
          if (!info.renderedLength && !/\.css(?:\?|$)/.test(id)) continue;
          const root = modulePackage(id);
          if (root) roots.add(root);
        }
      }
      const index = new NoticeIndex();
      for (const root of roots) {
        const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
        index.add(`${pkg.name}@${pkg.version}`, licenseTexts(root));
      }
      if (!roots.size) throw new Error("未识别到 Web 依赖");
      this.emitFile({
        type: "asset",
        fileName: "third-party-notices.json",
        source: index.serialize(),
      });
    },
  };
}
