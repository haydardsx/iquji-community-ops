import { createHash } from "node:crypto";
import fs from "node:fs";
import path from "node:path";

const licenseFile =
  /^(?:licen[sc]e|copying|notice|copyright|patents|unlicense|third[-_ ]party[-_ ](?:licenses|notices))(?:[._ -].*)?$/i;

export function licenseTexts(root: string): string[] {
  const texts: string[] = [];
  const read = (name: string) => {
    const text = fs.readFileSync(name, "utf8").trim();
    if (text && !text.includes("\u0000")) texts.push(text);
  };
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    if (entry.isFile() && licenseFile.test(entry.name)) read(path.join(root, entry.name));
    if (entry.isDirectory() && /^(?:licen[sc]es?|notices)$/i.test(entry.name)) {
      for (const item of fs.readdirSync(path.join(root, entry.name), { withFileTypes: true })) {
        if (item.isFile()) read(path.join(root, entry.name, item.name));
      }
    }
  }
  return texts;
}

export class NoticeIndex {
  private readonly notices = new Map<string, string>();
  private readonly components = new Map<string, string[]>();

  add(component: string, texts: string[]) {
    if (!texts.length) throw new Error(`组件缺少许可正文：${component}`);
    const ids = new Set(this.components.get(component) ?? []);
    for (const text of texts) {
      const hash = createHash("sha256").update(text).digest("hex");
      ids.add(hash);
      this.notices.set(hash, text);
    }
    this.components.set(component, [...ids].sort());
  }

  serialize() {
    const sorted = <T>(map: Map<string, T>) =>
      Object.fromEntries([...map].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)));
    return JSON.stringify(
      { components: sorted(this.components), notices: sorted(this.notices) },
      null,
      2,
    );
  }
}
