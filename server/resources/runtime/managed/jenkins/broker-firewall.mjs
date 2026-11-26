import { command, fail } from './broker-common.mjs';

const backends = ['iptables-nft', 'iptables-legacy'];
const firewallError = 'JENKINS_BROKER_FIREWALL_UNAVAILABLE';
const socketError = 'JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE';

async function forwardsThroughDockerUser(binary, execute) {
  const list = chain => execute(binary, ['-w', '5', '-S', chain], { timeout: 10000 });
  try {
    await list('INPUT');
    await list('DOCKER-USER');
    const forward = await list('FORWARD');
    // 前置放行规则可能绕开执行器的隔离；Docker 默认把无条件跳转放在首位。
    return forward.split('\n').find(line => line.startsWith('-A ')) === '-A FORWARD -j DOCKER-USER';
  } catch {
    return false;
  }
}

export async function detectBrokerFirewall(execute = command) {
  let driver;
  try {
    driver = await execute('docker', ['--host', 'unix:///var/run/docker.sock', 'info', '--format', '{{with .FirewallBackend}}{{.Driver}}{{end}}'], { timeout: 15000 });
  } catch {
    fail(socketError);
  }
  // 较早的 daemon 没有后端字段，此时仍按内核实际挂接的转发链判断。
  if (driver && driver !== 'iptables') fail(firewallError);
  const active = [];
  for (const binary of backends) {
    if (await forwardsThroughDockerUser(binary, execute)) active.push(binary);
  }
  if (active.length !== 1) fail(firewallError);
  return active[0];
}

if (import.meta.main) {
  try {
    console.log(await detectBrokerFirewall());
  } catch (error) {
    console.error(error.message === socketError ? socketError : firewallError);
    process.exitCode = 1;
  }
}
