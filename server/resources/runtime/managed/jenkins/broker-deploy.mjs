import { syncRegistryImages } from './broker-registry.mjs';
import { pullImageReference, verifyDaemonMirrors } from './broker-isolation.mjs';
import { declaredVolumes, ensureNodeVolumes, mountArguments } from './broker-volumes.mjs';
import { createReadStream } from 'node:fs';
import { mkdir, writeFile } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import path from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { command, fail, quote, root } from './broker-common.mjs';

// 健康检查镜像必须固定到网关已预置的摘要，避免构建完成后因拉取不存在的旧摘要而超时。
export const healthProbeImage = 'curlimages/curl@sha256:3fb7e13e5d179a845c97d78204f3f1064d1907037f26a3ebf91710571151fd93';

function sshArguments(run, target, script) {
  return ['-T', '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=accept-new', '-o', `UserKnownHostsFile=${root}/known_hosts`, '-o', 'ConnectTimeout=15', '-o', 'ServerAliveInterval=15', '-i', path.join(root, 'runs', run.runId, 'keys', target.id), '-p', String(target.port), `${target.user}@${target.host}`, script];
}
const healthAttempts = 30;
const healthWindow = 180000;
const healthRetryExitCodes = new Set([6, 7, 22, 28, 52, 56]);
// 应用进程在镜像入口写 stdout/stderr；部署统一提供可读取、按容量轮转的日志驱动。
export const applicationLogPolicy = Object.freeze({ driver: 'json-file', maxSize: '10m', maxFiles: '3' });
function checkCancelled(run) { if (run.abort.signal.aborted) fail('JENKINS_RELEASE_CANCELLED'); }
function deploymentCommands(run, ops) {
  const execute = ops.command || command;
  async function remote(target, args, { code = 'JENKINS_DEPLOY_QUERY_FAILED', health = false, timeout = 180000 } = {}) {
    checkCancelled(run);
    try {
      const output = await execute('ssh', sshArguments(run, target, args.map(quote).join(' ')), { signal: run.abort.signal, timeout, code });
      checkCancelled(run);
      return output;
    } catch (error) {
      checkCancelled(run);
      // 只暴露稳定分类，远端诊断可能包含地址、环境变量或凭据。
      if (error.exitCode === 255) fail('JENKINS_DEPLOY_SSH_FAILED');
      if (code === 'APPLICATION_IMAGE_SHELL_CHECK_FAILED' && [126, 127].includes(error.exitCode)) fail('APPLICATION_IMAGE_SHELL_UNAVAILABLE');
      if (health && healthRetryExitCodes.has(error.exitCode)) fail('JENKINS_DEPLOY_HEALTH_NOT_READY');
      fail(code);
    }
  }
  async function inspect(target, args) {
    const output = await remote(target, args, { code: 'JENKINS_DEPLOY_INSPECT_FAILED' });
    try {
      const value = JSON.parse(output);
      if (!value || typeof value !== 'object') fail('JENKINS_DEPLOY_INSPECT_INVALID');
      return value;
    } catch { fail('JENKINS_DEPLOY_INSPECT_INVALID'); }
  }
  return { remote, inspect };
}
async function pause(run, milliseconds, ops) {
  checkCancelled(run);
  try { await (ops.sleep || ((ms, signal) => delay(ms, undefined, { signal })))(milliseconds, run.abort.signal); }
  catch { checkCancelled(run); fail('JENKINS_DEPLOY_WAIT_FAILED'); }
  checkCancelled(run);
}
async function loadImage(run, target, file, local) {
  return new Promise((resolve, reject) => {
    // 构建机本身就是一个部署目标时直接读取本地文件，避免通过公网 SSH 回环传输大镜像。
    const child = local
      ? spawn('docker', ['load', '--quiet'], { stdio: ['pipe', 'pipe', 'pipe'], signal: run.abort.signal })
      : spawn('ssh', sshArguments(run, target, 'docker load --quiet'), { stdio: ['pipe', 'pipe', 'pipe'], signal: run.abort.signal });
    const source = createReadStream(file);
    source.pipe(child.stdin);
    source.on('error', reject);
    child.stdin.on('error', () => {});
    child.stdout.resume(); child.stderr.resume();
    const timer = setTimeout(() => child.kill('SIGTERM'), 10 * 60 * 1000);
    child.on('error', reject);
    child.on('close', code => { clearTimeout(timer); source.destroy(); code === 0 ? resolve() : reject(new Error('JENKINS_IMAGE_DELIVERY_FAILED')); });
  });
}
export function serviceArgs(materials, service, variant, existing) {
  const labels = { 'iquops.owner': 'iquops', 'iquops.application-id': materials.applicationId, 'iquops.service-slot': service.slotKey, 'traefik.enable': 'true', 'traefik.swarm.network': materials.network, [`traefik.http.services.${service.serviceName}.loadbalancer.server.port`]: String(materials.containerPort) };
  const ports = service.ports ?? [{name:'web', protocol:'http', targetPort:materials.containerPort, publishedPort:service.publishedPort}];
  const http = ports.find(port => port.protocol === 'http');
  if (http) labels[`traefik.http.services.${service.serviceName}.loadbalancer.server.port`] = String(http.targetPort);
  else {
    labels['traefik.enable'] = 'false';
    delete labels[`traefik.http.services.${service.serviceName}.loadbalancer.server.port`];
  }
  for (const port of ports) labels[`iquops.port.${port.name}`] = `${port.protocol}/${port.targetPort}`;
  const volumes = declaredVolumes(materials, service);
  const args = ['docker', 'service', existing ? 'update' : 'create', '--no-resolve-image'];
  if (existing) args.push('--image', variant.image, '--force');
  else args.push('--name', service.serviceName, '--network', materials.network, '--constraint', `node.id==${service.nodeId}`);
  args.push('--replicas', String(service.replicas), '--update-order', volumes.length ? 'stop-first' : 'start-first', '--update-parallelism', '1', '--update-failure-action', volumes.length ? 'pause' : 'rollback', '--rollback-order', 'stop-first');
  args.push('--log-driver', applicationLogPolicy.driver, '--log-opt', `max-size=${applicationLogPolicy.maxSize}`, '--log-opt', `max-file=${applicationLogPolicy.maxFiles}`);
  for (const [key, value] of Object.entries(labels)) args.push(existing ? '--label-add' : '--label', `${key}=${value}`);
  if (existing) {
    for (const name of Object.keys(existing.Spec.Labels)) {
      if (name === 'iquops.access-weight' || (name.startsWith('iquops.port.') || name.startsWith('traefik.http.services.')) && !Object.hasOwn(labels,name)) args.push('--label-rm',name);
    }
    const template = existing.Spec.TaskTemplate;
    for (const item of template.ContainerSpec.Env || []) args.push('--env-rm', item.split('=')[0]);
    const targetConstraint = `node.id==${service.nodeId}`;
    const nodeConstraints = (template.Placement?.Constraints || []).filter(item => item.startsWith('node.id=='));
    if (!nodeConstraints.includes(targetConstraint)) {
      // 先加入目标节点约束，再移除旧节点约束，避免更新窗口暂时没有节点约束。
      args.push('--constraint-add', targetConstraint);
      for (const item of nodeConstraints) args.push('--constraint-rm', item);
    }
    if (template.ContainerSpec.Mounts?.length && !nodeConstraints.includes(targetConstraint)) fail('APPLICATION_VOLUME_NODE_CHANGE_UNSUPPORTED');
    if (!template.Networks?.some(item => item.Target === materials.networkId)) args.push('--network-add', materials.network);
  }
  for (const [key, value] of Object.entries(service.environment)) args.push(existing ? '--env-add' : '--env', `${key}=${value}`);
  const mappings = ports.filter(p=>p.publishedPort>0).map(p=>({target:p.targetPort,published:p.publishedPort,protocol:p.protocol==='http'?'tcp':p.protocol}));
  const current = existing?.Spec.EndpointSpec?.Ports || [];
  const selector = (old,p) => old.TargetPort===p.target && (old.Protocol||'tcp')===p.protocol && (old.PublishMode||'ingress')==='ingress';
  const matches = (old,p) => selector(old,p) && old.PublishedPort===p.published;
  for (const old of current) {
    if (mappings.some(p=>matches(old,p))) continue;
    if (mappings.some(p=>selector(old,p) && current.some(item=>matches(item,p)))) fail('JENKINS_DEPLOY_PORTS_CONFLICT');
    args.push('--publish-rm',`target=${old.TargetPort},protocol=${old.Protocol||'tcp'},mode=${old.PublishMode||'ingress'}`);
  }
  for (const p of mappings) if (!current.some(old=>matches(old,p))) {
    args.push(existing?'--publish-add':'--publish',`published=${p.published},target=${p.target}`+(p.protocol==='tcp'?'':`,protocol=${p.protocol}`));
  }
  args.push(...mountArguments(volumes, existing, materials.applicationId, service));
  const startup = materials.command || [];
  const container = existing?.Spec.TaskTemplate.ContainerSpec;
  const override = materials.shellEntrypoint === true;
  const parameters = override ? startup.slice(1) : startup;
  if (override || container?.Command?.length) args.push('--entrypoint', override ? startup[0] : '');
  if (existing && (parameters.length || container?.Args?.length)) args.push('--args', parameters.map(quote).join(' '));
  args.push(existing ? service.serviceName : variant.image);
  if (!existing) args.push(...parameters);
  return args;
}
async function verifyHealth(run, gateway, pipeline, remote, notify, ops) {
  const managedProbe = run.materials.healthCommand?.length || run.materials.initializeCommand?.length;
  if (managedProbe) {
    for (const svc of run.materials.services) {
      const target = run.materials.targets.find(item => item.id === svc.serverId);
      const ids = await remote(target,['docker','ps','--filter',`label=com.docker.swarm.service.name=${svc.serviceName}`,'--format','{{.ID}}']);
      const containers=ids.trim().split(/\s+/).filter(Boolean);
      if (containers.length!==svc.replicas) fail('JENKINS_DEPLOY_HEALTH_NOT_READY');
      for (const id of containers) {
        if (!/^[a-f0-9]+$/.test(id)) fail('JENKINS_DEPLOY_INSPECT_INVALID');
        const probe=async()=> {
          if (!run.materials.healthCommand?.length) return;
          for(let attempt=0;attempt<healthAttempts;attempt++){
            try {await remote(target,['docker','exec',id,...run.materials.healthCommand],{code:'JENKINS_DEPLOY_CONTAINER_NOT_READY',timeout:10000});return}
            catch(e){if(e.message!=='JENKINS_DEPLOY_CONTAINER_NOT_READY')throw e}
            if(attempt+1<healthAttempts)await pause(run,2000,ops);
          }
          fail('JENKINS_DEPLOY_HEALTH_TIMEOUT');
        };
        await probe();
        if (run.materials.initializeCommand?.length) {
          await remote(target,['docker','exec',id,...run.materials.initializeCommand],{code:'APPLICATION_INITIALIZATION_FAILED'});
          await probe();
        }
      }
    }
    return;
  }
  const now = ops.now || Date.now;
  const deadline = now() + healthWindow;
  for (let attempt = 0; attempt < healthAttempts; attempt++) {
    checkCancelled(run);
    let ready = true;
    for (const service of run.materials.services) {
      // 无端口作业已由调用方核验实际副本、镜像和节点。
      if (Array.isArray(service.ports) && service.ports.length === 0) continue;
      const remaining = deadline - now();
      if (remaining <= 0) fail('JENKINS_DEPLOY_HEALTH_TIMEOUT');
      try {
        await remote(gateway, ['docker', 'run', '--rm', '--network', run.materials.network, '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', pullImageReference(healthProbeImage, run.materials.gatewayPull?.pullMirror ?? run.materials.source?.registryMirrorUrl), '--fail', '--silent', '--output', '/dev/null', '--max-time', '15', `http://${service.serviceName}:${service.ports?.find(p=>p.protocol==='http')?.targetPort || run.materials.containerPort}${pipeline.healthPath}`], { health: true, code: 'JENKINS_DEPLOY_HEALTH_PROBE_FAILED', timeout: Math.min(20000, remaining) });
      } catch (error) {
        if (error.message !== 'JENKINS_DEPLOY_HEALTH_NOT_READY') throw error;
        ready = false;
        break;
      }
    }
    if (ready) return;
    if (attempt === healthAttempts - 1 || deadline <= now()) break;
    if (attempt === 0) notify('服务尚未就绪，正在重试健康检查；请检查容器端口与健康路径', 'health_retry', 'warn');
    await pause(run, Math.min(2000, deadline - now()), ops);
  }
  fail('JENKINS_DEPLOY_HEALTH_TIMEOUT');
}
export async function deployImages(run, variants, pipeline, notify, ops = {}) {
  const materials = run.materials;
  const { remote, inspect } = deploymentCommands(run, ops);
  checkCancelled(run);
  // 所有平台都通过仓库回读后，才允许对部署节点进行操作。
  await syncRegistryImages(run, variants, message => notify(message, 'registry_sync'), ops.command, ops.runRoot);
  const directory = path.join(root, 'runs', run.runId, 'keys');
  await (ops.mkdir || mkdir)(directory, { recursive: true, mode: 0o700 });
  for (const target of materials.targets) {
    if (!/^[a-zA-Z0-9_.-]+$/.test(target.user) || !/^[a-zA-Z0-9.:-]+$/.test(target.host) || !Number.isInteger(target.port) || target.port < 1 || target.port > 65535) fail('JENKINS_DEPLOY_TARGET_INVALID');
    await (ops.writeFile || writeFile)(path.join(directory, target.id), target.key, { mode: 0o600 });
  }
  const gateway = materials.targets.find(item => item.id === materials.deploymentManagerServerId);
  if (!gateway) fail('JENKINS_DEPLOYMENT_MANAGER_REQUIRED');
  if (materials.gatewayPull?.verifyDaemon) {
    verifyDaemonMirrors(materials.gatewayPull, await remote(gateway, ['docker', 'info', '--format', '{{json .RegistryConfig.Mirrors}}']));
  }
  const swarm = await inspect(gateway, ['docker', 'info', '--format', '{{json .Swarm}}']);
  if (!swarm.ControlAvailable || swarm.LocalNodeState !== 'active' || swarm.Cluster?.ID !== materials.clusterId) fail('JENKINS_SWARM_CLUSTER_MISMATCH');
  const networks = await inspect(gateway, ['docker', 'network', 'inspect', materials.network]);
  const network = networks[0];
  if (network?.Driver !== 'overlay' || network?.Scope !== 'swarm') fail('JENKINS_OVERLAY_REQUIRED');
  materials.networkId = network.Id;
  const existingServices = new Map();
  for (const service of materials.services) {
    const nodes = await inspect(gateway, ['docker', 'node', 'inspect', service.nodeId]);
    if (nodes[0]?.Status?.State !== 'ready' || nodes[0]?.Spec?.Labels?.['iquops.server-id'] !== service.serverId) fail('JENKINS_NODE_OWNERSHIP_MISMATCH');
    const names = await remote(gateway, ['docker', 'service', 'ls', '--filter', `name=${service.serviceName}`, '--format', '{{.Name}}']);
    if (names.split('\n').includes(service.serviceName)) {
      const existing = (await inspect(gateway, ['docker', 'service', 'inspect', service.serviceName]))[0];
      const labels = existing?.Spec?.Labels;
      if (labels?.['iquops.owner'] !== 'iquops' || labels?.['iquops.application-id'] !== materials.applicationId || labels?.['iquops.service-slot'] !== service.slotKey || !existing?.Spec?.Mode?.Replicated) fail('JENKINS_SERVICE_OWNERSHIP_MISMATCH');
      existingServices.set(service.serviceName, existing);
    }
    const node = materials.targets.find(target => target.id === service.serverId);
    await ensureNodeVolumes(materials, service, existingServices.get(service.serviceName), node, { remote, inspect });
  }
  run.stage = 'delivery';
  const bySlot = new Map();
  const delivered = new Map();
  const artifacts = [];
  for (const service of materials.services) {
    const target = materials.targets.find(item => item.id === service.serverId);
    const variant = variants.find(item => item.platform === service.platform && item.slots.includes(service.slotKey));
    if (!target || !variant) fail('JENKINS_DEPLOY_TARGET_INVALID');
    bySlot.set(service.slotKey, variant);
    const delivery = JSON.stringify([target.id, variant.image]);
    if (!delivered.has(delivery)) {
      checkCancelled(run);
      await (ops.loadImage || loadImage)(run, target, variant.file, target.id === materials.buildServerId);
      const image = (await inspect(target, ['docker', 'image', 'inspect', variant.image]))[0];
      // 经典镜像存储返回配置摘要，containerd 镜像存储可返回清单描述符的摘要。
      const matchesManifest = variant.manifestDigest && image?.Id === variant.manifestDigest && image.Descriptor?.digest === variant.manifestDigest;
      if (image?.Id !== variant.digest && !matchesManifest) fail('JENKINS_IMAGE_DIGEST_MISMATCH');
      if (`${image.Os}/${image.Architecture}` !== variant.platform) fail('JENKINS_IMAGE_PLATFORM_MISMATCH');
      const entrypoint = image.Config?.Entrypoint;
      // 固定镜像入口负责解释 CMD 参数，只有直接作为启动程序的脚本才依赖对应的 Shell。
      const hasEntrypoint = entrypoint?.length && !(entrypoint.length === 1 && entrypoint[0] === '');
      const startup = materials.shellEntrypoint === true || !hasEntrypoint ? materials.command : undefined;
      const shells = new Set([startup, materials.healthCommand, materials.initializeCommand]
        .filter(argv => argv?.length >= 3 && /^(?:.*\/)?(?:sh|bash|dash|ash|ksh|zsh)$/.test(argv[0]) && argv[1].startsWith('-'))
        .map(argv => argv[0]));
      for (const shell of shells) {
        await remote(target, ['docker', 'run', '--rm', '--network', 'none', '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges', '--entrypoint', shell, variant.image, '-c', ':'], { code: 'APPLICATION_IMAGE_SHELL_CHECK_FAILED' });
      }
      delivered.set(delivery, image.Id);
    }
    artifacts.push({ serviceSlotKey: service.slotKey, serverId: target.id, platform: variant.platform, image: variant.image,
      imageDigest: delivered.get(delivery), configDigest: variant.digest, manifestDigest: variant.manifestDigest,
      ...(variant.registryImage && { registryImage: variant.registryImage, registryDigest: variant.registryDigest }) });
  }
  run.stage = 'deploy';
  for (const service of materials.services) {
    const variant = bySlot.get(service.slotKey);
    await remote(gateway, serviceArgs(materials, service, variant, existingServices.get(service.serviceName)), { code: 'JENKINS_DEPLOY_SERVICE_FAILED' });
  }
  run.stage = 'verify';
  for (let attempt = 0; attempt < 90; attempt++) {
    let ready = true;
    for (const service of materials.services) {
      const current = (await inspect(gateway, ['docker', 'service', 'inspect', service.serviceName]))[0];
      if (!current?.Spec) fail('JENKINS_DEPLOY_INSPECT_INVALID');
      const logging = current.Spec.TaskTemplate?.LogDriver;
      if (logging?.Name !== applicationLogPolicy.driver || logging.Options?.['max-size'] !== applicationLogPolicy.maxSize || logging.Options?.['max-file'] !== applicationLogPolicy.maxFiles) fail('JENKINS_DEPLOY_LOGGING_MISMATCH');
      if (['paused', 'rollback_started', 'rollback_paused', 'rollback_completed'].includes(current.UpdateStatus?.State)) fail('JENKINS_DEPLOY_ROLLED_BACK');
      const taskIDs = await remote(gateway, ['docker', 'service', 'ps', '--filter', 'desired-state=running', '-q', '--no-trunc', service.serviceName]);
      const tasks = taskIDs ? await inspect(gateway, ['docker', 'inspect', '--type', 'task', ...taskIDs.split('\n')]) : [];
      if (!Array.isArray(tasks)) fail('JENKINS_DEPLOY_INSPECT_INVALID');
      const variant = bySlot.get(service.slotKey);
      ready &&= tasks.length === service.replicas && tasks.every(task => task?.Status?.State === 'running' && task.NodeID === service.nodeId && task.Spec?.ContainerSpec?.Image?.split('@')[0] === variant.image);
    }
    if (ready) {
      await verifyHealth(run, gateway, pipeline, remote, notify, ops);
      run.artifacts = artifacts;
      run.imageDigest = [...new Set(artifacts.map(artifact => artifact.imageDigest))].join(',');
      const probes = materials.healthCommand?.length || materials.initializeCommand?.length || materials.services.some(service => service.ports == null || service.ports.length > 0);
      notify(probes ? '所有服务已在指定节点运行并通过健康检查' : '所有服务的副本、节点和镜像均已核验', 'health_confirmed', 'info');
      return;
    }
    await pause(run, 2000, ops);
  }
  fail('JENKINS_DEPLOY_CONVERGENCE_TIMEOUT');
}
