import { describe, expect, it } from "vitest";
import { sshConnectCommand } from "./ssh-command";

describe("SSH 快捷连接", () => {
  it("保留缺省用户和端口，IPv6 主机作为单独的参数", () => {
    expect(sshConnectCommand({ publicHost: "2001:db8::1" })).toBe(
      "ssh -p '22' -l 'root' -- '2001:db8::1'",
    );
  });

  it("空格和单引号保留在各自的参数内", () => {
    expect(
      sshConnectCommand({ port: 2222, sshUser: "example'user", publicHost: "demo host" }),
    ).toBe("ssh -p '2222' -l 'example'\\''user' -- 'demo host'");
  });
});
