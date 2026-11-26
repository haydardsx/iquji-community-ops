import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { SecretAsset, Server } from "@/contracts/types";
import { renderToStaticMarkup } from "@/test/render";
import { GitTokenFormDrawer } from "./GitTokenFormDrawer";
import { SecretDetailDrawer } from "./SecretDetailDrawer";
import { SecretRotationDrawer } from "./SecretRotationDrawer";
import { SshKeyFormDrawer } from "./SshKeyFormDrawer";

const selection = vi.hoisted(() => ({
  options: [] as Array<{ value: string; disabled?: boolean }>,
}));

vi.mock("@iquji-community/web-component", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    IqujiMultiSelect: ({
      options,
      label,
    }: {
      options: Array<{ value: string; disabled?: boolean }>;
      label?: string;
    }) => {
      selection.options = options;
      return <div>{label}</div>;
    },
    IqujiDrawer: ({
      open,
      title,
      description,
      children,
      actions,
      headerMeta,
      headerToolbar,
    }: {
      open: boolean;
      title: React.ReactNode;
      description?: React.ReactNode;
      children: React.ReactNode;
      actions?: React.ReactNode;
      headerMeta?: React.ReactNode;
      headerToolbar?: React.ReactNode;
    }) =>
      open ? (
        <div data-testid="iquji-drawer">
          <div data-testid="drawer-title">{title}</div>
          {description ? <div data-testid="drawer-desc">{description}</div> : null}
          {headerMeta ? <div data-testid="drawer-meta">{headerMeta}</div> : null}
          {headerToolbar ? <div data-testid="drawer-toolbar">{headerToolbar}</div> : null}
          <div data-testid="drawer-body">{children}</div>
          {actions ? <div data-testid="drawer-actions">{actions}</div> : null}
        </div>
      ) : null,
    useIqujiNotification: () => ({ open: vi.fn() }),
  };
});

vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false, submit: vi.fn() }),
}));

vi.mock("@/shared/ui/use-operation-feedback", () => ({
  useOperationNotice: () => vi.fn(),
  useOperationError: vi.fn(),
}));

const mockServers: Server[] = [
  {
    id: "server_example",
    name: "示例节点",
    publicHost: "192.0.2.1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
  },
];

const mockGitAsset: SecretAsset = {
  id: "asset_git_1",
  assetType: "git_token",
  name: "示例 Git 凭据",
  secretRef: "ref_demo_git",
  maskedSummary: "Git token **** 01234567",
  fingerprint: "0123456789ab",
  materialStatus: "available",
  description: "读取示例仓库",
  createdAt: "2026-11-16T10:00:00Z",
  updatedAt: "2026-11-16T10:00:00Z",
};

const mockSshAsset: SecretAsset = {
  id: "asset_ssh_1",
  assetType: "ssh_key",
  name: "示例 SSH 凭据",
  secretRef: "ref_demo_ssh",
  maskedSummary: "ssh-ed25519 **** 1234abcd",
  fingerprint: "1234abcd5678",
  materialStatus: "available",
  description: "连接示例主机",
  createdAt: "2026-11-16T10:00:00Z",
  updatedAt: "2026-11-16T10:00:00Z",
};

function renderWithProviders(element: React.ReactElement) {
  const client = new QueryClient();
  client.setQueryData(["secret-delete-preview", "asset_git_1"], {
    blocked: false,
    usage: {
      servers: [],
      gitApplications: ["示例应用"],
      applicationVariables: [],
    },
  });
  client.setQueryData(["secret-delete-preview", "asset_ssh_1"], {
    blocked: false,
    usage: {
      servers: ["示例节点"],
      gitApplications: [],
      applicationVariables: [],
    },
  });
  return renderToStaticMarkup(<QueryClientProvider client={client}>{element}</QueryClientProvider>);
}

describe("SshKeyFormDrawer 专有 SSH 私钥抽屉", () => {
  it("独立渲染私钥字段与服务器绑定，不包含类型切换单选框", () => {
    const markup = renderWithProviders(
      <SshKeyFormDrawer onOpenChange={() => {}} open servers={mockServers} />,
    );
    expect(markup).toContain("新建 SSH 私钥");
    expect(markup).toContain("私钥名称");
    expect(markup).toContain("绑定服务器");
    expect(markup).toContain("私钥内容");
    expect(markup).toContain("保存 SSH 私钥");
    expect(markup).not.toContain("Token 访问令牌");
    expect(markup).not.toContain("凭据类型");
  });
});

describe("GitTokenFormDrawer 专有 Git 凭据抽屉", () => {
  it("独立渲染 Git 令牌字段，不包含服务器绑定和类型切换单选框", () => {
    const markup = renderWithProviders(<GitTokenFormDrawer onOpenChange={() => {}} open />);
    expect(markup).toContain("新建 Git 凭据");
    expect(markup).toContain("凭据名称");
    expect(markup).toContain("Token 访问令牌");
    expect(markup).toContain("保存 Git 凭据");
    expect(markup).not.toContain("绑定服务器");
    expect(markup).not.toContain("OpenSSH");
    expect(markup).not.toContain("凭据类型");
  });
});

describe("SecretDetailDrawer 凭据详情", () => {
  it.each(["pending", "missing"])("%s 材料禁新增绑定，仍允许解除已有绑定", (materialStatus) => {
    renderWithProviders(
      <SecretDetailDrawer
        asset={{ ...mockSshAsset, materialStatus }}
        onChanged={() => {}}
        onOpenChange={() => {}}
        servers={[
          { ...mockServers[0], secretAssetIds: [mockSshAsset.id] },
          { ...mockServers[0], id: "server_new_example", secretAssetIds: [] },
        ]}
      />,
    );
    expect(selection.options.find((option) => option.value === mockServers[0].id)?.disabled).toBe(
      false,
    );
    expect(
      selection.options.find((option) => option.value === "server_new_example")?.disabled,
    ).toBe(true);
  });

  it("Git 凭据显示引用、材料指纹和关联应用，并允许保存或取消", () => {
    const markup = renderWithProviders(
      <SecretDetailDrawer
        asset={mockGitAsset}
        assets={[mockGitAsset]}
        onChanged={() => {}}
        onOpenChange={() => {}}
        servers={mockServers}
      />,
    );
    expect(markup).toContain("Secret Ref");
    expect(markup).toContain(String(mockGitAsset.secretRef));
    expect(markup).toContain("材料指纹");
    expect(markup).toContain(String(mockGitAsset.fingerprint));

    expect(markup).toContain("关联代码应用");
    expect(markup).not.toContain("绑定服务器");

    expect(markup).not.toContain("删除凭据");
    expect(markup).not.toContain("轮换材料");
    expect(markup).toContain("保存修改");
    expect(markup).toContain("取消");
  });

  it("SSH 私钥详情提供服务器绑定", () => {
    const markup = renderWithProviders(
      <SecretDetailDrawer
        asset={mockSshAsset}
        assets={[mockSshAsset]}
        onChanged={() => {}}
        onOpenChange={() => {}}
        servers={mockServers}
      />,
    );
    expect(markup).toContain("绑定服务器");
    expect(selection.options.map((option) => option.value)).toContain(mockServers[0].id);
  });
});

describe("SecretRotationDrawer 独立材料轮换抽屉", () => {
  it("显示当前指纹、轮换时间和新令牌输入框", () => {
    const markup = renderWithProviders(
      <SecretRotationDrawer asset={mockGitAsset} onOpenChange={() => {}} open />,
    );
    expect(markup).toContain(`轮换凭据 · ${mockGitAsset.name}`);
    expect(markup).toContain("当前材料指纹");
    expect(markup).toContain(String(mockGitAsset.fingerprint));
    expect(markup).toContain("上次轮换时间");
    expect(markup).toContain("新的 Git 访问令牌 (Token)");
    expect(markup).toContain("确认轮换凭据");

    expect(markup).not.toContain("删除凭据");
  });
});
