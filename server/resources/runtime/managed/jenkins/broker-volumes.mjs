import { posix } from 'node:path';
import { fail } from './broker-common.mjs';

const logicalName = /^[a-z][a-z0-9-]{0,39}$/;

export function declaredVolumes(materials, service) {
  const volumes = materials.volumes ?? [];
  if (!Array.isArray(volumes)) fail('APPLICATION_VOLUME_INVALID');
  if (!volumes.length) return [];
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(materials.applicationId)) fail('APPLICATION_VOLUME_INVALID');
  if (service.replicas !== 1 || (materials.services && materials.services.length !== 1)) fail('APPLICATION_PERSISTENT_SERVICE_REQUIRES_SINGLE_NODE');
  const names = new Set();
  const paths = new Set();
  let invalidPath = false;
  const result = [];
  for (const volume of volumes) {
    if (!volume || Object.keys(volume).some(key => key !== 'name' && key !== 'target')) fail('APPLICATION_VOLUME_INVALID');
    const { name, target } = volume;
    if (typeof name !== 'string' || !logicalName.test(name) || typeof target !== 'string' || !posix.isAbsolute(target) || target === '/' || /[\0\r\n,]/.test(target)) fail('APPLICATION_VOLUME_INVALID');
    const normalized = posix.resolve('/', target);
    if (names.has(name)) fail('APPLICATION_VOLUME_NAME_DUPLICATE');
    if (paths.has(normalized)) fail('APPLICATION_VOLUME_TARGET_DUPLICATE');
    names.add(name);
    paths.add(normalized);
    if (normalized !== target) invalidPath = true;
    result.push({ source: `iquops-${materials.applicationId}-${name}`, target });
  }
  if (invalidPath) fail('APPLICATION_VOLUME_INVALID');
  return result;
}

function existingTargets(existing, appID, nodeID) {
  const template = existing?.Spec?.TaskTemplate;
  const mounts = template?.ContainerSpec?.Mounts || [];
  if (mounts.length && !template.Placement?.Constraints?.includes(`node.id==${nodeID}`)) fail('APPLICATION_VOLUME_NODE_CHANGE_UNSUPPORTED');
  const targets = new Map();
  const prefix = `iquops-${appID}-`;
  for (const mount of mounts) {
    if (mount.Type !== 'volume' || !mount.Source?.startsWith(prefix) || !logicalName.test(mount.Source.slice(prefix.length)) || targets.has(mount.Source)) fail('APPLICATION_VOLUME_OWNERSHIP_MISMATCH');
    targets.set(mount.Source, mount.Target);
  }
  return targets;
}

// Docker 的 --mount 使用 CSV 字段，普通卷路径中的双引号也必须保留原义。
export function mountArguments(volumes, existing, applicationId, service) {
  const previous = existingTargets(existing, applicationId, service.nodeId);
  const args = [];
  for (const [source, target] of previous) {
    if (!volumes.some(volume => volume.source === source && volume.target === target)) args.push('--mount-rm', target);
  }
  for (const { source, target } of volumes) {
    if (previous.get(source) === target) continue;
    const field = target.includes('"') ? `"target=${target.replaceAll('"', '""')}"` : `target=${target}`;
    args.push(existing ? '--mount-add' : '--mount', `type=volume,source=${source},${field}`);
  }
  return args;
}

export async function ensureNodeVolumes(materials, service, existing, target, { remote, inspect }) {
  const volumes = declaredVolumes(materials, service);
  const previous = existingTargets(existing, materials.applicationId, service.nodeId);
  if (!volumes.length) return;
  if (!target || (await remote(target, ['docker', 'info', '--format', '{{.Swarm.NodeID}}'])).trim() !== service.nodeId) fail('APPLICATION_NODE_OWNERSHIP_MISMATCH');
  const names = new Set((await remote(target, ['docker', 'volume', 'ls', '--format', '{{.Name}}'], { code: 'APPLICATION_VOLUME_QUERY_FAILED' })).split(/\s+/));
  for (const { source, target: destination } of volumes) {
    if (!names.has(source)) {
      if (previous.has(source)) fail('APPLICATION_VOLUME_MISSING');
      await remote(target, ['docker', 'volume', 'create', '--driver', 'local', '--label', 'iquops.owner=iquops', '--label', `iquops.application-id=${materials.applicationId}`, source], { code: 'APPLICATION_VOLUME_CREATE_FAILED' });
    }
    // 同名创建可能返回并发创建者的旧卷，始终在实际目标节点回读并核对归属。
    const [actual] = await inspect(target, ['docker', 'volume', 'inspect', source]);
    if (actual?.Name !== source || actual.Driver !== 'local' || actual.Scope !== 'local' || Object.keys(actual.Options || {}).length) fail('APPLICATION_VOLUME_OWNERSHIP_MISMATCH');
    const owner = actual.Labels?.['iquops.owner'];
    const id = actual.Labels?.['iquops.application-id'];
    if (owner === 'iquops' && id === materials.applicationId) continue;
    if (owner || id || previous.get(source) !== destination) fail('APPLICATION_VOLUME_OWNERSHIP_MISMATCH');
  }
}
