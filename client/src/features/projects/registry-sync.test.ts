import { expect, it } from "vitest";
import type { SecretAsset } from "@/contracts/types";
import { applicationCredentialOptions, variableReferenceOptions } from "./ApplicationVariableTable";
import { applicationFormSchema, configPayload, formValues } from "./model";

it("Git 镜像仓库配置经过表单往返后保留，切换关闭时清除凭据引用", () => {
  const registrySync = {
    enabled: true,
    imageRepository: "registry.example.org/team/app",
    registryUsername: "demo",
    registrySecretRef: "credential",
  };
  const draft = formValues({ deliveryConfig: { registrySync } });
  expect(configPayload(applicationFormSchema.parse(draft)).deliveryConfig?.registrySync).toEqual(
    registrySync,
  );
  draft.registry.mode = "disabled";
  expect(configPayload(applicationFormSchema.parse(draft)).deliveryConfig?.registrySync).toEqual({
    enabled: false,
  });
});

it("启用认证时拒绝单独的用户名", () => {
  const draft = formValues({});
  draft.registry = {
    mode: "enabled",
    repository: "registry.example.org/team/app",
    username: "demo",
    secretRef: "",
  };
  expect(applicationFormSchema.safeParse(draft).success).toBe(false);
});

it("旧推送引用保留为不可用选项，选择仓库专用凭据后恢复发布", () => {
  const registrySync = {
    enabled: true,
    imageRepository: "registry.example.org/team/app",
    registrySecretRef: "old-variable",
  };
  const asset: SecretAsset = {
    id: "example-asset",
    name: "示例应用凭据",
    createdAt: "",
    updatedAt: "",
    assetType: "application_secret",
    materialStatus: "available",
    secretRef: "old-variable",
  };
  const assets = [
    asset,
    {
      ...asset,
      id: "example-push",
      name: "示例仓库凭据",
      assetType: "registry_secret",
      secretRef: "new-push",
    },
  ];
  const draft = formValues({ deliveryConfig: { registrySync } });
  expect(draft.registry.secretRef).toBe("old-variable");
  expect(applicationFormSchema.safeParse(draft).success).toBe(false);
  const options = applicationCredentialOptions(assets, "registry_secret");
  expect(variableReferenceOptions(options, draft.registry.secretRef, "不可用")).toEqual([
    { value: "new-push", label: "示例仓库凭据" },
    { value: "old-variable", label: "不可用", disabled: true },
  ]);
  expect(applicationCredentialOptions(assets)).toEqual([
    { value: "old-variable", label: "示例应用凭据" },
  ]);
  draft.registry.username = "demo";
  draft.registry.secretRef = "new-push";
  expect(configPayload(applicationFormSchema.parse(draft)).deliveryConfig?.registrySync).toEqual({
    ...registrySync,
    registryUsername: "demo",
    registrySecretRef: "new-push",
  });
});
