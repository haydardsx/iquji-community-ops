import { randomUUID } from "node:crypto";

// 脚本自身的诊断使用标准错误流。子工具的输出与命令返回值由各自的协议决定。
export function diagnostic(level, event, message, errorCode) {
  const record = {
    id: randomUUID(),
    timestamp: new Date().toISOString(),
    level,
    source: "tool",
    event: `tool.${event}`,
    message,
  };
  if (errorCode) record.errorCode = errorCode;
  process.stderr.write(`${JSON.stringify(record)}\n`);
}
