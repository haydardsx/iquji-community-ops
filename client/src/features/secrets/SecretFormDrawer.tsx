import type { Server } from "@/contracts/types";
import { GitTokenFormDrawer } from "./GitTokenFormDrawer";
import { SshKeyFormDrawer } from "./SshKeyFormDrawer";

export interface SecretFormDrawerProps {
  open: boolean;
  servers: Server[];
  defaultType?: "ssh_key" | "git_token";
  onOpenChange: (open: boolean) => void;
}

/** 兼容历史调用的凭据创建抽屉包装器，按需分发至专用抽屉。 */
export function SecretFormDrawer({
  open,
  servers,
  defaultType = "ssh_key",
  onOpenChange,
}: SecretFormDrawerProps) {
  if (defaultType === "git_token") {
    return <GitTokenFormDrawer onOpenChange={onOpenChange} open={open} />;
  }
  return <SshKeyFormDrawer onOpenChange={onOpenChange} open={open} servers={servers} />;
}
