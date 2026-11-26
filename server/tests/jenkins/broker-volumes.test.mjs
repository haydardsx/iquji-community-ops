import test from 'node:test';
import assert from 'node:assert/strict';
import { declaredVolumes, ensureNodeVolumes, mountArguments } from '../../resources/runtime/managed/jenkins/broker-volumes.mjs';

const base = {
  applicationId: 'app_demo',
  services: [{ serviceName: 'demo', slotKey: 'main', nodeId: 'node-a', replicas: 1 }],
  volumes: [{ name: 'store', target: '/var/lib/app' }],
};
const oldService = {
  Spec: {
    Labels: { 'iquops.owner': 'iquops', 'iquops.application-id': 'app_demo' },
    TaskTemplate: {
      Placement: { Constraints: ['node.id==node-a'] },
      ContainerSpec: { Mounts: [{ Type: 'volume', Source: 'iquops-app_demo-store', Target: '/var/lib/app' }] },
    },
  },
};

test('部署材料验证卷名、绝对挂载路径及单实例约束', () => {
  assert.deepEqual(declaredVolumes(base, base.services[0]), [{ source: 'iquops-app_demo-store', target: '/var/lib/app' }]);
  for (const volumes of [
    [{ name: 'store', target: '/data' }, { name: 'store', target: '/backup' }],
    [{ name: 'store', target: '/data' }, { name: 'backup', target: '/data' }],
    [{ name: 'store', target: '/data/../data' }],
    [{ name: 'store', target: '/data//files' }],
    [{ name: 'store', target: '/' }],
  ]) assert.throws(() => declaredVolumes({ ...base, volumes }, base.services[0]), { message: /APPLICATION_VOLUME_/ });
  assert.throws(() => declaredVolumes(base, { ...base.services[0], replicas: 2 }), { message: 'APPLICATION_PERSISTENT_SERVICE_REQUIRES_SINGLE_NODE' });
});

test('服务更新保留精确同名挂载并移除配置中已删除的挂载', () => {
  assert.deepEqual(mountArguments(declaredVolumes(base, base.services[0]), oldService, base.applicationId, base.services[0]), []);
  const stale = structuredClone(oldService);
  stale.Spec.TaskTemplate.ContainerSpec.Mounts.push({ Type: 'volume', Source: 'iquops-app_demo-cache', Target: '/cache' });
  assert.deepEqual(mountArguments(declaredVolumes(base, base.services[0]), stale, base.applicationId, base.services[0]), ['--mount-rm', '/cache']);
  const invalidMount = structuredClone(oldService);
  invalidMount.Spec.TaskTemplate.ContainerSpec.Mounts[0].Type = 'bind';
  assert.throws(() => mountArguments(declaredVolumes(base, base.services[0]), invalidMount, base.applicationId, base.services[0]), { message: 'APPLICATION_VOLUME_OWNERSHIP_MISMATCH' });
});

test('卷在实际任务节点创建和核验，精准旧挂载允许接管无标签旧卷', async () => {
  const calls = [];
  const deps = {
    remote: async (target, args) => {
      calls.push([target.id, args]);
      if (args[1] === 'info') return 'node-a';
      if (args[1] === 'volume') return 'iquops-app_demo-store';
      throw new Error('unexpected remote command');
    },
    inspect: async (target, args) => {
      calls.push([target.id, args]);
      return [{ Name: args.at(-1), Driver: 'local', Scope: 'local', Options: {}, Labels: {} }];
    },
  };
  await ensureNodeVolumes(base, base.services[0], oldService, { id: 'server-a' }, deps);
  assert.ok(calls.every(([id]) => id === 'server-a'));
  assert.equal(calls.some(([, args]) => args[1] === 'create'), false);
});

test('孤立或其他应用所有的卷会阻断发布', async () => {
  const deps = {
    remote: async (_target, args) => args[1] === 'info' ? 'node-a' : '',
    inspect: async (_target, args) => [{ Name: args.at(-1), Driver: 'local', Scope: 'local', Options: {}, Labels: { 'iquops.owner': 'iquops', 'iquops.application-id': 'other' } }],
  };
  await assert.rejects(ensureNodeVolumes(base, base.services[0], undefined, { id: 'server-a' }, deps), { message: 'APPLICATION_VOLUME_OWNERSHIP_MISMATCH' });
});
