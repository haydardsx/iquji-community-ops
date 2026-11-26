import type { OperationRun, SecretAsset } from "@/contracts/types";
import type { ConfirmedOperationInput } from "@/shared/operations/confirmed-operation";

type SecretAssetOperation = { asset: SecretAsset; operation?: OperationRun };

/** 凭据创建只负责确认入队；材料生效与服务器绑定由 Worker 执行。 */
export class SecretCreationSession {
  private created: SecretAssetOperation | null = null;
  private confirmed: OperationRun | null = null;

  get hasAsset() {
    return this.created !== null;
  }

  get rejected() {
    return this.confirmed?.status === "failed" || this.confirmed?.status === "cancelled";
  }

  async save(input: {
    label: string;
    create: (requestKey: string) => Promise<SecretAssetOperation>;
    submit: (operation: ConfirmedOperationInput) => Promise<OperationRun>;
  }): Promise<boolean> {
    if (!this.confirmed) {
      this.confirmed = await input.submit({
        action: "create_secret_asset",
        targetKind: "secret_asset",
        label: input.label,
        create: async (requestKey) => {
          this.created ??= await input.create(requestKey);
          if (!this.created.operation) throw new Error("保存凭据后未返回操作记录。");
          return this.created.operation;
        },
      });
    }
    if (this.rejected) return false;
    if (!this.created) throw new Error("未找到本次保存的凭据，请关闭后重新打开。");
    return true;
  }
}
