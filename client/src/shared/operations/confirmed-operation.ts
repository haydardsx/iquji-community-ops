import type { OperationRun } from "@/contracts/types";
import { ApiError, fetchJson } from "@/shared/api/client";

export type ConfirmedOperationInput = {
  create: (requestKey: string) => Promise<OperationRun>;
  action: string;
  targetKind: string;
  targetId?: string;
  label: string;
};

type OperationTransport = {
  get: (id: string) => Promise<OperationRun>;
  confirm: (id: string) => Promise<OperationRun>;
  cancel: (id: string) => Promise<OperationRun>;
};

const transport: OperationTransport = {
  get: (id) => fetchJson(`operations/runs/${id}`),
  confirm: (id) => fetchJson(`operations/runs/${id}/confirm`, { method: "POST" }),
  cancel: (id) => fetchJson(`operations/runs/${id}/cancel`, { method: "POST" }),
};

export function isTerminalOperation(run: OperationRun) {
  return ["succeeded", "failed", "cancelled"].includes(run.status);
}

// 保留创建得到的 ID，使确认响应丢失时能够重试原操作。
export class ConfirmedOperationSession {
  operationId: string | null = null;
  private requestKey: string | null = null;
  private input: ConfirmedOperationInput | null = null;
  private busy = false;

  constructor(
    private readonly api: OperationTransport = transport,
    private readonly createRequestKey: () => string = () => crypto.randomUUID(),
  ) {}

  async submit(input: ConfirmedOperationInput): Promise<OperationRun> {
    if (this.busy) throw new Error("操作正在提交，请稍候。");
    if (this.requestKey && this.input && !this.matchesInput(input, this.input)) {
      throw new Error("请先重试或取消尚未提交的操作，再切换目标。");
    }
    this.busy = true;
    try {
      if (!this.operationId) {
        this.input = input;
        this.requestKey ??= this.createRequestKey();
        const run = await input.create(this.requestKey);
        this.checkTarget(run, input);
        this.operationId = run.id;
        if (isTerminalOperation(run)) {
          this.clear();
          return run;
        }
      }
      let confirmed: OperationRun;
      try {
        confirmed = await this.api.confirm(this.operationId);
      } catch (error) {
        if (!(error instanceof ApiError) || error.errorCode !== "TASK_NOT_CONFIRMABLE") {
          throw error;
        }
        // 只有 confirm 明确表示已离开待确认状态，才可用查询结果消歧。
        confirmed = await this.api.get(this.operationId);
      }
      this.checkTarget(confirmed, this.input ?? input);
      this.clear();
      return confirmed;
    } finally {
      this.busy = false;
    }
  }

  async cancel() {
    if (this.busy) throw new Error("操作正在提交，请稍候。");
    const id = this.operationId;
    if (!id) return;
    this.busy = true;
    try {
      await this.api.cancel(id);
      this.clear();
    } finally {
      this.busy = false;
    }
  }

  reset() {
    if (!this.busy && !this.operationId) this.clear();
  }

  private clear() {
    this.operationId = null;
    this.requestKey = null;
    this.input = null;
  }

  private matchesInput(a: ConfirmedOperationInput, b: ConfirmedOperationInput) {
    return a.action === b.action && a.targetKind === b.targetKind && a.targetId === b.targetId;
  }

  private checkTarget(run: OperationRun, input: ConfirmedOperationInput) {
    if (
      !run.id ||
      run.action !== input.action ||
      run.targetKind !== input.targetKind ||
      (input.targetId !== undefined && run.targetId !== input.targetId)
    ) {
      throw new Error("操作响应与当前目标不一致，未继续确认。");
    }
  }
}
