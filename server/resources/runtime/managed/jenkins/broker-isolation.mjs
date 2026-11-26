import {agentVariables,buildkitVariables,prepareBuildSecrets,secretMounts,secretLogFilter} from './broker-variables.mjs';
import { mkdir, writeFile, rm, readFile } from 'node:fs/promises';
import path from 'node:path';
import { lookup } from 'node:dns/promises';
import { command, docker, contained, root, fail } from './broker-common.mjs';
import { detectBrokerFirewall } from './broker-firewall.mjs';

const buildkitImage = 'moby/buildkit:v0.26.2-rootless@sha256:0ffa2fcf6b8757c47d569b3ef0f03f9d5eb3b9ff5ce68d858f994f89b749da0c';
const firewallBinary = () => process.env.IQUOPS_IPTABLES_BIN || 'iptables';
export function egressRules(subnet, blockedHosts = []) {
  const denied = ['0.0.0.0/8', '10.0.0.0/8', '127.0.0.0/8', '169.254.0.0/16', '172.16.0.0/12', '192.168.0.0/16', '224.0.0.0/4', ...blockedHosts.filter(host => /^\d{1,3}(\.\d{1,3}){3}$/.test(host))];
  return [['INPUT', '-s', subnet, '-j', 'REJECT'], ...denied.map(host => ['DOCKER-USER', '-s', subnet, '-d', host, '-j', 'REJECT']), ['DOCKER-USER', '-s', subnet, '-p', 'tcp', '-m', 'multiport', '!', '--dports', '80,443', '-j', 'REJECT']];
}
function privateBuildTarget(address, hosts) {
  const [first, second] = address.split('.').map(Number);
  return hosts.includes(address) || first === 0 || first === 10 || first === 127 ||
    (first === 169 && second === 254) || (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 168) || first >= 224;
}

function daemonMirrorList(output) {
  try {
    return (JSON.parse(output) || []).map(value => {
      const url = new URL(value);
      if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) fail('JENKINS_REGISTRY_MIRROR_INVALID');
      return value.replace(/\/+$/, '');
    });
  } catch { fail('JENKINS_REGISTRY_MIRROR_INVALID'); }
}
export function verifyDaemonMirrors(frozen, output) {
  if (!frozen?.verifyDaemon) return;
  const observed = daemonMirrorList(output);
  const matched = frozen.origin === 'server'
    ? observed[0] === frozen.mirrors[0]
    : JSON.stringify(observed) === JSON.stringify(frozen.mirrors);
  if (!matched) fail('DOCKER_REGISTRY_MIRROR_DRIFT');
}

export async function prepareIsolation(run, persist = async () => {}) {
  // 安装后的 daemon 或规则也可能改变，运行期间只允许使用安装时核实的后端。
  if (await detectBrokerFirewall() !== firewallBinary()) fail('JENKINS_ISOLATION_FIREWALL_FAILED');
  const pull = run.materials.buildPull;
  if (pull?.verifyDaemon || pull?.origin === 'daemon') {
    const output = await docker(['info', '--format', '{{json .RegistryConfig.Mirrors}}']);
    if (pull.verifyDaemon) verifyDaemonMirrors(pull, output);
    else {
      const observed = daemonMirrorList(output);
      pull.mirrors = observed;
      pull.verifyDaemon = true;
      run.materials.registryMirrors = observed;
      run.materials.serverRegistryMirrors = observed;
    }
  }
  const network = `iquops-build-${run.runId}`;
  await docker(['network', 'create', '--driver', 'bridge', '--opt', 'com.docker.network.bridge.enable_icc=false', '--label', 'iquops.owner=iquops', network]);
  run.network = network;
  run.cleanup = { pending: true, network, firewallRules: [] };
  await persist();
  const inspected = JSON.parse(await docker(['network', 'inspect', network]));
  const subnet = inspected[0]?.IPAM?.Config?.[0]?.Subnet;
  if (!subnet || !/^\d+\.\d+\.\d+\.\d+\/\d+$/.test(subnet)) fail('JENKINS_ISOLATION_NETWORK_INVALID');
  const hosts = [run.materials.buildHost, ...run.materials.targets.map(target => target.host)];
  run.firewallRules = egressRules(subnet, hosts);
  // 私网例外限于服务器已登记的镜像；应用配置的专用端口只对该镜像的公网地址开放。
  const trustedMirrors = run.materials.source?.registryMirrorUrl
    ? run.materials.serverRegistryMirrors || []
    : run.materials.registryMirrors || [];
  for (const mirror of run.materials.registryMirrors || []) {
    const url = new URL(mirror);
    const port = url.port || (url.protocol === 'http:' ? '80' : '443');
    const trusted = trustedMirrors.includes(mirror);
    if (!trusted && port === '443') continue;
    const hostname = url.hostname.replace(/^\[|\]$/g, '');
    const addresses = await lookup(hostname, { family: 4, all: true });
    for (const { address } of addresses) {
      if (!trusted && privateBuildTarget(address, hosts)) continue;
      run.firewallRules.push(['DOCKER-USER', '-s', subnet, '-d', address, '-p', 'tcp', '--dport', port, '-j', 'ACCEPT']);
    }
  }
  run.firewallRules = run.firewallRules.map(rule => [...rule, '-m', 'comment', '--comment', `iquops.release-run=${run.runId}`]);
  run.cleanup.firewallRules = run.firewallRules;
  await persist();
  for (const rule of run.firewallRules) await command(firewallBinary(), ['-w', '-I', rule[0], '1', ...rule.slice(1)], { code: 'JENKINS_ISOLATION_FIREWALL_FAILED' });
}
// Docker daemon 的单次拉取通过镜像坐标选择覆盖源，BuildKit 单独使用 registry 配置。
export function pullImageReference(reference, override) {
  if (!override) return reference;
  const [first, ...rest] = reference.split('/');
  let image = reference;
  if (rest.length && /^(docker\.io|index\.docker\.io)$/.test(first)) image = rest.join('/');
  else if (rest.length && (/[.:]/.test(first) || first === 'localhost')) return reference;
  if (!image.includes('/')) image = 'library/' + image;
  const mirror = new URL(override);
  const authority = /[:.]/.test(mirror.host) || mirror.host === 'localhost' ? mirror.host : mirror.host + ':443';
  return authority + mirror.pathname.replace(/\/$/, '') + '/' + image;
}

export function agentArgs(run, pipeline, stage) {
  return ['run', '--rm', '--name', `${run.network}-agent`, '--network', run.network, '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--user', '1000:1000', '--read-only', '--pids-limit', '512', '--memory', '2g', '--cpus', '2', '--tmpfs', '/tmp:rw,nosuid,nodev,size=512m', '--mount', `type=bind,source=${run.sourceDirectory},target=/workspace`, '--workdir', '/workspace', ...agentVariables(run), ...secretMounts(run), '--env', 'CI=true', '--env', 'HOME=/tmp', '--env', `BUILD_REVISION=${run.commitSha}`, '--label', 'iquops.owner=iquops', '--label', `iquops.release-run=${run.runId}`, '--entrypoint', '/bin/sh', pullImageReference(pipeline.nodeImage, run.materials?.buildPull?.pullMirror ?? run.materials?.source?.registryMirrorUrl), '-eu', '-c', pipeline[stage]];
}
export async function executeAgent(run, pipeline, notify, stageLog) {
  await prepareBuildSecrets(run);
  for (const [stage, name] of [['installCommand', 'install'], ['testCommand', 'test'], ['buildCommand', 'compile']]) {
    run.stage = name;
    stageLog(name, `正在执行 ${name}`);
    await docker(agentArgs(run, pipeline, stage), { logFilter:()=>secretLogFilter(run.materials), signal: run.abort.signal, log: notify, maxBytes: 32 * 1024 * 1024, code: 'JENKINS_PROJECT_STEP_FAILED' });
  }
}
export function buildkitConfiguration(mirrors, inheritServer = false) {
  const httpHosts = new Set();
  const values = mirrors.map(value => {
    const url = new URL(value);
    if ((url.protocol !== 'https:' && !(inheritServer && url.protocol === 'http:')) || url.username || url.password || url.search || url.hash) fail('JENKINS_REGISTRY_MIRROR_INVALID');
    if (url.protocol === 'http:') httpHosts.add(url.host);
    return url.host + url.pathname.replace(/\/$/, '');
  });
  return '[worker.oci]\n  snapshotter = "native"\n[registry."docker.io"]\n  mirrors = ' + JSON.stringify(values) + '\n' + [...httpHosts].map(host => '[registry.' + JSON.stringify(host) + ']\n  http = true\n').join('');
}
export async function buildImages(run, pipeline, notify, groupIndex) {
  const directory = run.buildDirectory || path.join(root, 'runs', run.runId);
  const output = path.join(directory, 'output');
  await mkdir(output, { recursive: true, mode: 0o700 });
  await command('chown', ['1000:1000', output]);
  const configFile = path.join(directory, 'buildkitd.toml');
  await writeFile(configFile, buildkitConfiguration(run.materials.registryMirrors || [], !run.materials.source?.registryMirrorUrl), { mode: 0o644 });
  await contained(run.sourceDirectory, pipeline.context);
  await contained(run.sourceDirectory, pipeline.dockerfile);
  const name = `${run.network}-buildkit-${groupIndex}`;
  const args = ['run', '-d', '--name', name, '--network', run.network, '--security-opt', 'seccomp=unconfined', '--security-opt', 'systempaths=unconfined', '--pids-limit', '1024', '--memory', '3g', '--cpus', '2', '--label', 'iquops.owner=iquops', '--label', `iquops.release-run=${run.runId}`, '--mount', `type=bind,source=${run.sourceDirectory},target=/workspace,readonly`, '--mount', `type=bind,source=${output},target=/output`, ...secretMounts(run), '--mount', `type=bind,source=${configFile},target=/home/user/.config/buildkit/buildkitd.toml,readonly`, pullImageReference(buildkitImage, run.materials.buildPull?.pullMirror ?? run.materials.source?.registryMirrorUrl), '--oci-worker-snapshotter=native'];
  await docker(args, { signal: run.abort.signal, code: 'JENKINS_ROOTLESS_BUILDER_UNAVAILABLE' });
  let ready = false;
  for (let index = 0; index < 30; index++) {
    try { await docker(['exec', name, 'buildctl', 'debug', 'workers'], { timeout: 5000 }); ready = true; break; } catch { await new Promise(resolve => setTimeout(resolve, 1000)); }
    if (run.abort.signal.aborted) fail('JENKINS_RELEASE_CANCELLED');
  }
  if (!ready) fail('JENKINS_ROOTLESS_BUILDER_UNAVAILABLE');
  run.stage = 'image';
  const variants = [];
  const platforms = [...new Set(run.materials.services.map(service => service.platform))];
  for (const [index, platform] of platforms.entries()) {
    if (!['linux/amd64', 'linux/arm64'].includes(platform)) fail('JENKINS_TARGET_PLATFORM_UNSUPPORTED');
    const image = `iquops/${run.materials.applicationId.toLowerCase()}:${run.runId.toLowerCase()}-${groupIndex}-${platform.split('/')[1]}`;
    await docker(['exec', name, 'buildctl', 'build', '--frontend', 'dockerfile.v0', '--local', `context=/workspace/${pipeline.context}`, '--local', 'dockerfile=/workspace', '--opt', `filename=${pipeline.dockerfile}`, '--opt', `platform=${platform}`, ...buildkitVariables(run), '--opt', `build-arg:BUILD_REVISION=${run.commitSha}`, '--output', `type=docker,name=${image},dest=/output/image-${index}.tar`, '--metadata-file', `/output/image-${index}.json`], { signal: run.abort.signal, maxBytes: 32 * 1024 * 1024, logFilter:()=>secretLogFilter(run.materials), log: notify, code: 'JENKINS_IMAGE_BUILD_FAILED', timeout: 60 * 60 * 1000 });
    const metadata = JSON.parse(await readFile(path.join(output, `image-${index}.json`), 'utf8'));
    const digest = metadata['containerimage.config.digest'];
    const manifestDigest = metadata['containerimage.digest'];
    if (![digest, manifestDigest].every(value => /^sha256:[a-f0-9]{64}$/.test(value || ''))) fail('JENKINS_IMAGE_DIGEST_INVALID');
    const slots = run.materials.services.filter(service => service.platform === platform).map(service => service.slotKey);
    variants.push({ slots, platform, image, digest, manifestDigest, file: path.join(output, `image-${index}.tar`) });
  }
  await docker(['rm', '-f', '--volumes', name], { code: 'JENKINS_CLEANUP_REQUIRED' });
  return variants;
}
export async function cleanupIsolation(run, ops = {}) {
  try {
    const runDocker = ops.docker || docker;
    const runCommand = ops.command || command;
    const owned = ['ps', '-aq', '--filter', `label=iquops.release-run=${run.runId}`];
    const ids = (await runDocker(owned, { timeout: 30000 })).split('\n').filter(Boolean);
    if (ids.some(id => !/^[a-f0-9]{12,64}$/.test(id))) fail('JENKINS_CLEANUP_REQUIRED');
    if (ids.length) await runDocker(['rm', '-f', '--volumes', ...ids], { timeout: 30000 });
    if (await runDocker(owned, { timeout: 30000 })) fail('JENKINS_CLEANUP_REQUIRED');
    if (run.network) {
      const networks = (await runDocker(['network', 'ls', '--format', '{{.Name}}'], { timeout: 30000 })).split('\n');
      if (networks.includes(run.network)) {
        await runDocker(['network', 'rm', run.network], { timeout: 30000 });
        const remaining = (await runDocker(['network', 'ls', '--format', '{{.Name}}'], { timeout: 30000 })).split('\n');
        if (remaining.includes(run.network)) fail('JENKINS_CLEANUP_REQUIRED');
      }
    }
    // 注释绑定运行标识，状态文件丢失时也能找回该次执行的规则。
    const rules = [...run.firewallRules || []];
    for (const chain of ['INPUT', 'DOCKER-USER']) {
      const listing = await runCommand(firewallBinary(), ['-w', '-S', chain], { timeout: 10000 });
      for (const line of String(listing || '').split('\n')) {
        const args = line.trim().split(/\s+/).map(value => value.replace(/^"|"$/g, ''));
        const comment = args.indexOf('--comment');
        if (args[0] === '-A' && args[1] === chain && comment >= 0 && args[comment + 1] === `iquops.release-run=${run.runId}`) {
          const rule = args.slice(1);
          if (!rules.some(existing => JSON.stringify(existing) === JSON.stringify(rule))) rules.push(rule);
        }
      }
    }
    // Docker 确认网络已移除后再撤防火墙规则，失败时保持出站封锁。
    for (const rule of rules) {
      let present = true;
      try { await runCommand(firewallBinary(), ['-w', '-C', rule[0], ...rule.slice(1)], { timeout: 10000 }); }
      catch (error) { if (error.exitCode === 1) present = false; else throw error; }
      if (present) await runCommand(firewallBinary(), ['-w', '-D', rule[0], ...rule.slice(1)], { timeout: 10000 });
    }
    const directory = path.join(root, 'runs', run.runId);
    if (ops.removeFiles) await ops.removeFiles();
    else for (const name of ['registry', 'groups', 'build-secrets', 'source', 'output', 'buildkitd.toml', 'keys', 'git-token', 'git-auth', 'Jenkinsfile']) await rm(path.join(directory, name), { force: true, recursive: true });
    run.cleanup = { pending: false, network: '', firewallRules: [] };
    run.network = '';
    run.firewallRules = [];
  } catch { fail('JENKINS_CLEANUP_REQUIRED'); }
}
