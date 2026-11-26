import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { useForm } from "react-hook-form";
import { describe, expect, it } from "vitest";
import type { ApplicationSourceConfig } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationSourceSection } from "./ApplicationConfigSections";
import { type ApplicationForm, formValues } from "./model";

function SourceSectionHarness({
  sourcePath,
  source,
}: {
  sourcePath?: string;
  source?: ApplicationSourceConfig;
}) {
  const form = useForm<ApplicationForm>({
    defaultValues: formValues({
      sourceConfig: source ?? {
        provider: "git",
        repositoryUrl: "https://github.com/example/demo-app.git",
        refType: "branch",
        ref: "main",
        jenkinsfilePath: "Jenkinsfile",
        sourcePath: sourcePath ?? ".",
      },
    }),
  });
  return (
    <IqujiOverlayProvider>
      <ApplicationSourceSection disabled={false} form={form} secrets={[]} servers={[]} />
    </IqujiOverlayProvider>
  );
}

describe("应用来源与发布配置卡片", () => {
  it("Docker 来源独立显示镜像卡片", () => {
    const html = renderToStaticMarkup(
      <SourceSectionHarness source={{ provider: "image", image: "example/web:1" }} />,
    );
    expect(html).toContain("Docker 镜像");
    expect(html).toContain('name="source.image"');
    expect(html).not.toContain("Git 仓库地址");
  });

  it("既有 PyPI 配置按容器入口参数编辑", () => {
    const html = renderToStaticMarkup(
      <SourceSectionHarness
        source={{
          provider: "pypi",
          baseImage: "example/python:3",
          packageName: "sample",
          version: "1.0",
          command: ["serve"],
        }}
      />,
    );
    expect(html).toContain("PyPI");
    expect(html).toContain("包管理器");
    expect(html).toContain('name="source.baseImage"');
    expect(html).toContain("镜像默认启动命令");
    expect(html).toContain("ops-config-panel__body");
    expect(html).toContain("ops-package-coords-grid");
    expect(html).toContain("ops-command-embedded");
    expect(html).toContain("ops-command-field__body");
    expect(html).toContain("python:3.12-slim");
    expect(html).not.toContain("ops-command-card");
    expect(html).not.toContain("添加参数");
    expect(html).not.toContain("格式化");
    expect(html).not.toContain("Jenkinsfile");
  });

  it("将源码仓库与构建环境分为两张清晰卡片，并在收起高级设置时展示源码目录摘要", () => {
    const markup = renderToStaticMarkup(<SourceSectionHarness sourcePath="apps/backend" />);
    expect(markup).toContain("源码仓库");
    expect(markup).toContain("构建环境");
    expect(markup).toContain("Git 仓库地址");
    expect(markup).toContain("构建机器 (Jenkins 节点)");
    expect(markup).toContain("高级设置");
    expect(markup).toContain("源码目录：apps/backend");
    expect(markup).toContain("ops-source-grid--3");
    expect(markup).toContain("ops-source-grid--2");
  });
});
