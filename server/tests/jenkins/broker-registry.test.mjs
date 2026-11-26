import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { deployImages } from '../../resources/runtime/managed/jenkins/broker-deploy.mjs';
import { syncRegistryImages } from '../../resources/runtime/managed/jenkins/broker-registry.mjs';

for (const failure of ['', 'copy', 'tag', 'config']) {
  test('构建镜像同步及凭据回收：' + (failure || '核验成功'), async () => {
    const directory = await mkdtemp(path.join(tmpdir(), 'ops-registry-'));
    const digest = 'sha256:' + 'a'.repeat(64);
    const variant = { image: 'iquops/example:run-0-amd64', digest, file: '/output/image.tar' };
    const run = { runId: 'run', abort: new AbortController(), materials: { registrySync: {
      repository: 'registry.example.org/team/app', username: 'demo', password: 'test-only',
    } } };
    const auth = path.join(directory, 'runs', 'run', 'registry', 'auth.json');
    try {
      const execute = async (binary, args) => {
        assert.equal(binary, 'skopeo');
        assert.equal(args[args.indexOf('--retry-times') + 1], '2');
        assert.ok(!args.join(' ').includes('test-only'));
        assert.equal((await stat(auth)).mode & 0o777, 0o600);
        const auths = JSON.parse(await readFile(auth, 'utf8')).auths;
        assert.deepEqual(Object.keys(auths), ['registry.example.org']);
        if (args[0] === 'copy') {
          if (failure === 'copy') throw new Error('JENKINS_REGISTRY_PUSH_FAILED');
          await writeFile(args[args.indexOf('--digestfile') + 1], digest);
          return '';
        }
        if (args.includes('--raw')) return JSON.stringify({config:{digest: failure === 'config' ? 'bad' : digest}});
        return failure === 'tag' ? 'bad' : digest;
      };
      const result = syncRegistryImages(run, [variant], () => {}, execute, directory);
      if (failure) {
        await assert.rejects(result, /JENKINS_REGISTRY_(PUSH|VERIFY)_FAILED/);
        assert.equal(variant.registryImage, undefined);
      } else {
        await result;
        assert.equal(variant.registryImage, 'registry.example.org/team/app:run-0-amd64');
        assert.equal(variant.registryDigest, digest);
      }
      await assert.rejects(stat(auth), {code:'ENOENT'});
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
}


test('第二种平台未通过核验时，保留第一种平台的记录并阻止所有部署写入', async () => {
  const directory = await mkdtemp(path.join(tmpdir(), 'ops-registry-'));
  const digest = 'sha256:' + 'a'.repeat(64);
  const variants = ['amd64', 'arm64'].map(arch => ({
    image: 'iquops/example:run-0-' + arch, file: arch + '.tar', digest,
  }));
  const run = {runId: 'run', abort: new AbortController(), materials: {registrySync: {
    repository: 'registry.example.org/team/app',
  }}};
  const pushed = [], events = [];
  const command = async (binary, args) => {
    assert.equal(binary, 'skopeo', '校验通过前禁止向任何部署节点发起命令');
    if (args[0] === 'copy') {
      pushed.push(args.at(-1));
      await writeFile(args[args.indexOf('--digestfile') + 1], digest);
      return '';
    }
    if (args.includes('--raw')) return JSON.stringify({config: {digest: pushed.length === 1 ? digest : 'invalid'}});
    return digest;
  };
  try {
    await assert.rejects(deployImages(run, variants, {}, (message, event) => events.push([message, event]), {
      command, runRoot: directory, mkdir: () => assert.fail('推送失败后仍准备部署凭据'),
    }), /JENKINS_REGISTRY_VERIFY_FAILED/);
    assert.deepEqual(pushed, ['amd64', 'arm64'].map(arch => 'docker://registry.example.org/team/app:run-0-' + arch));
    assert.equal(variants[0].registryDigest, digest);
    assert.equal(variants[1].registryDigest, undefined);
    assert.ok(events.some(([message, event]) => event === 'registry_sync' && message.includes(variants[0].registryImage + '@' + digest)));
    await assert.rejects(stat(path.join(directory, 'runs', 'run', 'registry')), {code: 'ENOENT'});
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

for (const changed of [false, true]) {
  test('推送部分失败后按已记录的摘要复核既有平台：' + (changed ? '标签变化' : '同一制品'), async () => {
    const directory = await mkdtemp(path.join(tmpdir(), 'ops-registry-'));
    const architectures = ['amd64', 'arm64'];
    const images = architectures.map((arch, index) => ({
      image: 'iquops/example:run-0-' + arch, file: arch + '.tar', digest: 'sha256:' + String(index + 1).repeat(64),
    }));
    const run = {runId: 'run', abort: new AbortController(), materials: {registrySync: {repository: 'registry.example.org/team/app'}}};
    const copies = [], reads = [];
    let failure = true;
    const execute = async (binary, args) => {
      assert.equal(binary, 'skopeo');
      const reference = args.at(-1);
      if (args[0] === 'copy') {
        const arch = architectures.find(arch => reference.endsWith('-' + arch));
        copies.push(arch);
        if (arch === 'arm64' && failure) {
          failure = false;
          throw Error('JENKINS_REGISTRY_PUSH_FAILED');
        }
        await writeFile(args[args.indexOf('--digestfile') + 1], images[architectures.indexOf(arch)].digest);
        return '';
      }
      if (args.includes('--raw')) return JSON.stringify({config: {digest: reference.split('@').at(-1)}});
      const arch = architectures.find(arch => reference.endsWith('-' + arch));
      reads.push(arch);
      if (changed && !failure && arch === 'amd64') return 'sha256:' + 'f'.repeat(64);
      return images[architectures.indexOf(arch)].digest;
    };
    try {
      const attempt = syncRegistryImages(run, images, () => {}, execute, directory);
      if (changed) {
        await assert.rejects(attempt, /JENKINS_REGISTRY_VERIFY_FAILED/);
        assert.deepEqual(copies, ['amd64', 'arm64']);
        assert.deepEqual(reads, ['amd64', 'amd64']);
      } else {
        await attempt;
        assert.deepEqual(copies, ['amd64', 'arm64', 'arm64']);
        assert.deepEqual(reads, ['amd64', 'amd64', 'arm64']);
        const resumed = images.map(({image, file, digest}) => ({image, file, digest}));
        await syncRegistryImages(run, resumed, () => {}, execute, directory);
        assert.deepEqual(copies, ['amd64', 'arm64', 'arm64']);
        assert.deepEqual(resumed, images);
      }
      await assert.rejects(stat(path.join(directory, 'runs', 'run', 'registry')), {code:'ENOENT'});
      const file = path.join(directory, 'runs', 'run', 'registry-progress.json');
      const facts = Object.values(JSON.parse(await readFile(file, 'utf8')));
      assert.equal(facts.length, changed ? 1 : 2);
      assert.deepEqual(Object.keys(facts[0]).sort(), ['configDigest', 'manifestDigest']);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
}
