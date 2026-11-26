import { describe, expect, it } from "vitest";
import type { ApplicationDeploymentConfig } from "@/contracts/types";
import {
  packageManagers,
  sourceFormSchema,
  sourceFormValues,
  sourcePayload,
  sourceSchema,
} from "./application-source";
import { commandDraft } from "./command-arguments";
import { applicationFormSchema, configPayload, formValues } from "./model";

describe("应用来源与容器表单往返", () => {
  it("地址中的身份信息、查询参数和片段不能进入配置", () => {
    const form = sourceFormValues();
    for (const address of [
      "https://example.test/repo.git?sample=value",
      "ssh://example.test/repo.git#sample",
      "https://user@example.test/repo.git",
    ]) {
      form.repositoryUrl = address;
      expect(sourceFormSchema.safeParse(form).success).toBe(false);
      expect(sourceSchema.safeParse(sourcePayload(form)).success).toBe(false);
    }
    for (const address of ["https://example.test/repo.git", "ssh://example.test/repo.git"]) {
      form.repositoryUrl = address;
      expect(sourceFormSchema.safeParse(form).success).toBe(true);
      expect(sourceSchema.safeParse(sourcePayload(form)).success).toBe(true);
    }
  });
  it("Git 与镜像的覆盖源往返，清空时恢复继承", () => {
    const form = sourceFormValues();
    form.repositoryUrl = "https://git.example.test/project.git";
    form.image = "alpine:3.21";
    for (const provider of ["git", "image"] as const) {
      form.provider = provider;
      form.registryMirrorUrl = "https://mirror.example.test:8443/cache";
      const source = sourceSchema.parse(sourcePayload(sourceFormSchema.parse(form)));
      expect(sourceFormValues(source).registryMirrorUrl).toBe(form.registryMirrorUrl);
      form.registryMirrorUrl = "http://mirror.example.test";
      expect(sourceFormSchema.safeParse(form).success).toBe(false);
      form.registryMirrorUrl = "";
      expect(sourcePayload(form)).not.toHaveProperty("registryMirrorUrl");
    }
  });
  it.each(packageManagers)("%s 只发送所选包来源字段并保留启动参数边界", (provider) => {
    const source = {
      provider,
      baseImage: "example/base:1",
      packageName: "example",
      version: "1.2.3",
      command: ["serve", "a b", ""],
    };
    const form = { ...sourceFormValues(source), image: "ignored:1", repositoryUrl: "unused" };
    expect(form.provider).toBe("package");
    expect(form.packageManager).toBe(provider);
    expect(sourcePayload(sourceFormSchema.parse(form))).toEqual(source);
  });
  it("表单与 API 同时拒绝不固定的镜像、版本范围和错误来源的字段", () => {
    const image = { provider: "image" as const, image: "example/service:latest" };
    const pkg = {
      provider: "npm" as const,
      baseImage: "example/node:20",
      packageName: "demo",
      version: "^1.0.0",
      command: ["serve"],
    };
    for (const config of [image, pkg]) {
      expect(sourceSchema.safeParse(config).success).toBe(false);
      expect(sourceFormSchema.safeParse(sourceFormValues(config)).success).toBe(false);
    }
    expect(
      sourceSchema.safeParse({
        ...image,
        image: "example/service:1",
        repositoryUrl: "https://example.test/repo",
      }).success,
    ).toBe(false);
    expect(sourceSchema.safeParse({ ...pkg, version: "1.2.3", command: [""] }).success).toBe(false);
  });

  it("参数草稿可纠正，来源切换保留包管理器且只提交当前来源字段", () => {
    const form = formValues({
      sourceConfig: {
        provider: "pypi",
        baseImage: "example/python:3",
        packageName: "sample",
        version: "1.0",
        command: ["serve"],
      },
    });
    form.source.command = commandDraft(["", "a b"]);
    expect(applicationFormSchema.safeParse(form).success).toBe(false);
    expect(form.source.command.text).toEqual("'' 'a b'");
    form.source.provider = "image";
    form.source.image = "example/web:1";
    expect(configPayload(applicationFormSchema.parse(form)).sourceConfig).toEqual({
      provider: "image",
      image: "example/web:1",
    });
    form.source.provider = "package";
    expect(form.source.packageManager).toBe("pypi");
    form.source.command.text = "serve 'a b'";
    for (const field of ["command", "initializeCommand", "healthCommand"] as const) {
      form[field] = commandDraft(["", "argument"]);
      expect(applicationFormSchema.safeParse(form).success).toBe(false);
      form[field].text = "program argument";
    }
    expect(configPayload(applicationFormSchema.parse(form)).sourceConfig).toMatchObject({
      provider: "pypi",
      command: ["serve", "a b"],
    });
  });

  it("从 Git 切到镜像后移除 Git 认证与构建服务器", () => {
    const source = {
      provider: "git" as const,
      repositoryUrl: "https://example.test/repo.git",
      ref: "v1",
      refType: "tag" as const,
      sourcePath: ".",
      jenkinsfilePath: "Jenkinsfile",
      authSecretRef: "secret_example",
    };
    const form = formValues({ sourceConfig: source, buildServerId: "server_example" });
    expect(sourcePayload(form.source)).toEqual(source);
    form.source.provider = "image";
    form.source.image = "example/web:1";
    const saved = configPayload(applicationFormSchema.parse(form));
    expect(saved.sourceConfig).toEqual({ provider: "image", image: "example/web:1" });
    expect(saved.buildServerId).toBeFalsy();
  });
  it("多端口模板保留端口、挂载、检查及初始化，删除命令不会恢复草稿基线", () => {
    const config: ApplicationDeploymentConfig = {
      sourceConfig: { provider: "image", image: "example/service:1" },
      swarmRuntime: {
        ports: [
          { name: "web", protocol: "http", targetPort: 80 },
          { name: "ssh", protocol: "tcp", targetPort: 22, publishedPort: 2222 },
        ],
        volumes: [{ name: "data", target: "/data" }],
        command: ["serve", "two words"],
        healthCommand: ["test", "-d", "/data"],
        initializeCommand: ["setup"],
      },
    };
    const form = formValues(config);
    form.initializeCommand.text = "";
    const saved = configPayload(applicationFormSchema.parse(form), config);
    expect(saved.swarmRuntime).toMatchObject({
      ...config.swarmRuntime,
      initializeCommand: undefined,
    });
    expect(saved.swarmRuntime?.containerPort).toBeUndefined();
  });
});
