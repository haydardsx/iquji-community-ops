import { z } from "zod";
import type { ApplicationSourceConfig } from "@/contracts/types";
import {
  commandArguments,
  commandDraft,
  commandDraftSchema,
  commandValue,
} from "./command-arguments";

export const packageManagers = ["apt", "dnf", "yum", "npm", "pypi"] as const;
export const packageManagerLabels = {
  apt: "APT",
  dnf: "DNF",
  yum: "YUM",
  npm: "npm",
  pypi: "PyPI",
};
const imagePattern =
  /^([a-z0-9][a-z0-9.-]*(:[0-9]+)?\/)?[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}|@sha256:[0-9a-f]{64})$/;
const pinnedImage = z
  .string()
  .refine(
    (value) => imagePattern.test(value) && !value.includes("//") && !value.endsWith(":latest"),
    "请填写带固定版本标签或摘要的镜像",
  );
const packageName = z
  .string()
  .regex(/^(@[a-z0-9][a-z0-9._-]*\/)?[a-zA-Z0-9][a-zA-Z0-9._+-]*$/, "请填写有效包名");
const exactVersion = z.string().regex(/^[0-9][a-zA-Z0-9.:_+~%-]*$/, "请填写精确版本号");
const gitURLHint = "使用不含身份信息、查询参数或片段的 HTTPS 或 SSH Git 地址";
const gitURL = z.string().refine((value) => {
  if (!/^(https|ssh):\/\//.test(value) || /[?#\s\\]/.test(value)) return false;
  try {
    const url = new URL(value);
    return Boolean(url.hostname && !url.username && !url.password && url.pathname.length > 1);
  } catch {
    return false;
  }
}, gitURLHint);
const mirrorHint = "请使用不含凭据、查询参数或片段的 HTTPS Docker Hub 镜像源";
const registryMirror = z
  .string()
  .trim()
  .refine((value) => {
    if (!value) return true;
    try {
      const url = new URL(value);
      return (
        /^https:\/\//.test(value) &&
        !/[?#\s\\]/.test(value) &&
        !url.username &&
        !url.password &&
        url.port !== "0" &&
        !/:(?:\/|$)/.test(value.slice("https://".length)) &&
        !value.includes("..") &&
        /^(?:[a-z0-9]+(?:[.-][a-z0-9-]+)*|\[[0-9a-f:]+\])(?::[0-9]+)?$/.test(url.host) &&
        /^(\/[a-z0-9]+(?:[._-][a-z0-9]+)*)*\/?$/.test(url.pathname)
      );
    } catch {
      return false;
    }
  }, mirrorHint);
export const sourceSchema = z.union([
  z
    .object({
      provider: z.literal("git"),
      repositoryUrl: gitURL,
      ref: z.string(),
      refType: z.enum(["branch", "tag", "commit"]),
      jenkinsfilePath: z.string(),
      sourcePath: z.string(),
      registryMirrorUrl: registryMirror.optional(),
      authSecretRef: z.string().optional(),
    })
    .strict(),
  z
    .object({
      provider: z.literal("image"),
      image: pinnedImage,
      registryMirrorUrl: registryMirror.optional(),
    })
    .strict(),
  z
    .object({
      provider: z.enum(packageManagers),
      baseImage: pinnedImage,
      packageName,
      version: exactVersion,
      command: commandArguments,
      shellEntrypoint: z.literal(true).optional(),
    })
    .strict()
    .refine((source) => source.provider === "npm" || !source.packageName.startsWith("@"), {
      path: ["packageName"],
      message: "只有 npm 包支持作用域",
    }),
]);

export const sourceFormSchema = z
  .object({
    provider: z.enum(["git", "image", "package"]),
    packageManager: z.enum(packageManagers),
    image: z.string().trim(),
    registryMirrorUrl: z.string().trim(),
    repositoryUrl: z.string().trim(),
    ref: z.string().trim(),
    refType: z.enum(["branch", "tag", "commit"]),
    jenkinsfilePath: z.string().trim(),
    sourcePath: z.string().trim(),
    authSecretRef: z.string(),
    baseImage: z.string().trim(),
    packageName: z.string().trim(),
    version: z.string().trim(),
    command: commandDraftSchema,
  })
  .superRefine((source, ctx) => {
    const issue = (field: keyof typeof source, message: string) =>
      ctx.addIssue({ code: "custom", path: [field], message });
    if (
      source.provider !== "package" &&
      !registryMirror.safeParse(source.registryMirrorUrl).success
    )
      issue("registryMirrorUrl", mirrorHint);
    if (source.provider === "git") {
      if (source.repositoryUrl && !gitURL.safeParse(source.repositoryUrl).success)
        issue("repositoryUrl", gitURLHint);
      for (const field of ["ref", "sourcePath", "jenkinsfilePath"] as const)
        if (!source[field]) issue(field, "请填写源码配置");
    } else if (source.provider === "image") {
      if (!pinnedImage.safeParse(source.image).success)
        issue("image", "请填写固定版本或摘要的镜像");
    } else {
      if (!pinnedImage.safeParse(source.baseImage).success)
        issue("baseImage", "请填写固定版本或摘要的基础镜像");
      if (
        !packageName.safeParse(source.packageName).success ||
        (source.packageManager !== "npm" && source.packageName.startsWith("@"))
      )
        issue("packageName", "请填写有效包名");
      if (!exactVersion.safeParse(source.version).success) issue("version", "请填写精确版本号");
      if (!commandArguments.safeParse(commandValue(source.command).argv).success)
        issue("command", "请填写有效的启动参数");
    }
  });

export function sourceFormValues(
  source?: ApplicationSourceConfig,
): z.infer<typeof sourceFormSchema> {
  const git = source?.provider === "git" ? source : undefined;
  const pkg =
    source && source.provider !== "git" && source.provider !== "image" ? source : undefined;
  return {
    provider: pkg ? "package" : source?.provider === "image" ? "image" : "git",
    packageManager: pkg?.provider ?? "apt",
    repositoryUrl: git?.repositoryUrl ?? "",
    ref: git?.ref ?? "main",
    refType: git?.refType ?? "branch",
    sourcePath: git?.sourcePath ?? ".",
    jenkinsfilePath: git?.jenkinsfilePath ?? "Jenkinsfile",
    authSecretRef: git?.authSecretRef ?? "",
    image: source?.provider === "image" ? source.image : "",
    registryMirrorUrl:
      source && "registryMirrorUrl" in source ? (source.registryMirrorUrl ?? "") : "",
    baseImage: pkg?.baseImage ?? "",
    packageName: pkg?.packageName ?? "",
    version: pkg?.version ?? "",
    command: commandDraft(pkg?.command, pkg?.shellEntrypoint),
  };
}

export function sourcePayload(
  source: z.infer<typeof sourceFormSchema>,
): ApplicationSourceConfig | undefined {
  const {
    provider,
    packageManager,
    image,
    repositoryUrl,
    ref,
    refType,
    jenkinsfilePath,
    sourcePath,
    authSecretRef,
    baseImage,
    packageName,
    version,
    command,
  } = source;
  const mirror = source.registryMirrorUrl ? { registryMirrorUrl: source.registryMirrorUrl } : {};
  if (provider === "git")
    return repositoryUrl
      ? {
          provider,
          repositoryUrl,
          ...mirror,
          ref,
          refType,
          jenkinsfilePath,
          sourcePath,
          authSecretRef: authSecretRef || undefined,
        }
      : undefined;
  if (provider === "image") return { provider, image, ...mirror };
  const { argv, shellEntrypoint } = commandValue(command);
  return {
    provider: packageManager,
    baseImage,
    packageName,
    version,
    command: argv,
    ...(shellEntrypoint && { shellEntrypoint }),
  };
}
