import type { Server } from "@/contracts/types";

function quoteArgument(value: string) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

export function sshConnectCommand(server: Pick<Server, "port" | "publicHost" | "sshUser">) {
  const port = quoteArgument(String(server.port ?? 22));
  const user = quoteArgument(server.sshUser ?? "root");
  return `ssh -p ${port} -l ${user} -- ${quoteArgument(server.publicHost)}`;
}
