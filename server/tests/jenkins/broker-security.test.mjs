import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, symlink, rm, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { contained, publicDiagnostic, publicRun, safeRelative, quote, sameSecret, validatePipeline } from '../../resources/runtime/managed/jenkins/broker-common.mjs';
import { agentArgs, egressRules, buildkitConfiguration, pullImageReference, verifyDaemonMirrors } from '../../resources/runtime/managed/jenkins/broker-isolation.mjs';
import { healthProbeImage, serviceArgs } from '../../resources/runtime/managed/jenkins/broker-deploy.mjs';
import { persistRunState, restoreRunState, recoverRunState } from '../../resources/runtime/managed/jenkins/broker-state.mjs';
import { cleanupIsolation } from '../../resources/runtime/managed/jenkins/broker-isolation.mjs';
import { checkoutSource, stopRunJob } from '../../resources/runtime/managed/jenkins/broker-source.mjs';

const pipeline = { nodeImage: 'node:22-bookworm-slim', installCommand: 'npm ci', testCommand: 'npm test', buildCommand: 'npm run build', dockerfile: 'Dockerfile', context: '.', healthPath: '/health' };
test('发布失败摘要仅包含已定义的诊断码', () => {
  const fallback = 'JENKINS_RELEASE_FAILED';
  assert.equal(publicDiagnostic('JENKINS_GIT_FETCH_FAILED'), 'JENKINS_GIT_FETCH_FAILED');
  for (const summary of ['JENKINS_EXAMPLE_LABEL', 'ENOENT: sample path']) {
    const run = { runId: 'sample', status: 'failed', summary };
    assert.equal(publicDiagnostic(summary), fallback);
    assert.equal(publicRun(run).summary, fallback);
    assert.equal(restoreRunState(run, run.runId).summary, fallback);
  }
});
test('项目配置不能注入部署目标或控制地址', () => {
  for (const key of ['targets', 'environment', 'callbackUrl', 'token', 'privileged', 'volumes']) assert.throws(() => validatePipeline({ ...pipeline, [key]: 'attacker' }), /CONFIG_INVALID/);
  for (const value of ['../Dockerfile', '/etc/shadow', 'nested/../../escape', 'nested\\..\\escape']) assert.throws(() => safeRelative(value), /PATH_INVALID/);
  assert.deepEqual(validatePipeline(pipeline), pipeline);
});
test('源码符号链接不能读取工作目录外的材料', async () => {
  const base = await mkdtemp(path.join(os.tmpdir(), 'iquops-source-'));
  try {
    await mkdir(path.join(base, 'source'));
    await writeFile(path.join(base, 'control'), 'secret');
    await symlink('../control', path.join(base, 'source', 'Jenkinsfile'));
    await assert.rejects(contained(path.join(base, 'source'), 'Jenkinsfile'), /PATH_INVALID/);
  } finally { await rm(base, { recursive: true, force: true }); }
});
test('检出前拒绝仓库地址中附带的认证信息', async () => {
  for (const url of ['https://example.test/sample.git?token=example', 'https://example.test/sample.git#example']) {
    await assert.rejects(checkoutSource({ materials: { source: { repositoryUrl: url } } }), /JENKINS_SOURCE_INVALID/);
  }
  await assert.rejects(checkoutSource({ materials: { source: { repositoryUrl: 'ssh://example.test/sample.git' } } }), /JENKINS_GIT_SSH_NOT_CONFIGURED/);
});
test('项目执行容器没有宿主凭据或Docker控制能力', () => {
  const run = { network: 'iquops-build-test', sourceDirectory: '/workspace/source', commitSha: 'a'.repeat(40), runId: 'run_test' };
  const args = agentArgs(run, pipeline, 'testCommand');
  for (const value of ['--user', '1000:1000', '--cap-drop', 'ALL', '--read-only', '--pids-limit']) assert.ok(args.includes(value));
  const wire = args.join(' ');
  for (const value of ['docker.sock', 'SSH', 'TOKEN', 'control.json', '--privileged', '--network host']) assert.ok(!wire.includes(value));
  assert.equal(args.at(-1), 'npm test');
  assert.ok(egressRules('198.51.100.0/24', ['203.0.113.10']).some(rule => rule.includes('203.0.113.10') && rule.includes('REJECT')));
  assert.ok(egressRules('198.51.100.0/24').some(rule => rule[0] === 'INPUT'));
});
test('镜像源配置禁止凭据与非HTTPS，旧内核使用native', () => {
  assert.match(buildkitConfiguration(['https://mirror.example/']), /snapshotter = "native"/);
  assert.match(buildkitConfiguration(['https://mirror.example/']), /\["mirror.example"\]/);
  assert.throws(() => buildkitConfiguration(['https://<user>:<password>@mirror.example/']), /MIRROR_INVALID/);
  assert.throws(() => buildkitConfiguration(['http://mirror.example/']), /MIRROR_INVALID/);
});
test('发布覆盖指向 Docker Hub 镜像，其他仓库保留来源', () => {
  const mirror = 'https://images.example.test:8443/proxy';
  assert.equal(pullImageReference('node:22', mirror), 'images.example.test:8443/proxy/library/node:22');
  assert.equal(pullImageReference('docker.io/node:22', mirror), 'images.example.test:8443/proxy/library/node:22');
  assert.equal(pullImageReference('moby/buildkit:1', mirror), 'images.example.test:8443/proxy/moby/buildkit:1');
  assert.equal(pullImageReference('other.example.test/runtime:1', mirror), 'other.example.test/runtime:1');
  assert.equal(pullImageReference('node:22', ''), 'node:22');
  assert.match(buildkitConfiguration(['http://images.example.test:5000'], true), /http = true/);
});
test('冻结的镜像设置按应用和节点隔离，HTTP 服务器配置核对 daemon 的首选项', () => {
  const first = { origin: 'server', verifyDaemon: true, mirrors: ['http://mirror.example.test:5000'] };
  verifyDaemonMirrors(first, '["http://mirror.example.test:5000/","https://other.example.test"]');
  assert.throws(() => verifyDaemonMirrors(first, '["https://other.example.test"]'), /MIRROR_DRIFT/);
  assert.throws(() => verifyDaemonMirrors({ origin: 'daemon', verifyDaemon: true, mirrors: [] }, '["https://new.example.test"]'), /MIRROR_DRIFT/);
  const args = ['a','b'].map(prefix => agentArgs({
    network: 'run-' + prefix, sourceDirectory: '/workspace',
    materials: {buildPull: {pullMirror: `https://mirror.example.test:8443/${prefix}`}}
  }, pipeline, 'installCommand'));
  assert.ok(args[0].includes('mirror.example.test:8443/a/library/node:22-bookworm-slim'));
  assert.ok(args[1].includes('mirror.example.test:8443/b/library/node:22-bookworm-slim'));
});
test('部署参数固定节点及归属，direct-load不解析外部仓库', () => {
  const materials = { applicationId: 'app_demo', network: 'iquops-public', containerPort: 3000 };
  const service = { serviceName: 'demo-one', slotKey: 'one', nodeId: 'node_one', replicas: 1, publishedPort: 0, environment: { VALUE: "a'; touch /tmp/injected; echo '" } };
  const args = serviceArgs(materials, service, { image: 'iquops/demo:run_one' });
  assert.ok(args.includes('--no-resolve-image'));
  assert.ok(args.includes('node.id==node_one'));
  assert.ok(args.includes('iquops.application-id=app_demo'));
  assert.ok(args.includes('traefik.enable=true'));
  assert.equal(args.some(value => value.startsWith('iquops.access-weight=')), false);
  assert.ok(args.includes('VALUE=' + service.environment.VALUE));
  assert.equal(quote("a'b"), "'a'\\''b'");
});
test('更新已有服务只在节点变化时调整约束且先加后删', () => {
  const materials = { applicationId: 'app_demo', network: 'iquops-public', networkId: 'network_demo', containerPort: 3000 };
  const service = { serviceName: 'demo-one', slotKey: 'one', nodeId: 'node_two', replicas: 1, publishedPort: 0, environment: {} };
  const existing = { Spec: { Labels: { 'iquops.owner': 'iquops', 'iquops.application-id': 'app_demo', 'iquops.service-slot': 'one' }, Mode: { Replicated: {} }, TaskTemplate: { ContainerSpec: { Env: [] }, Placement: { Constraints: ['node.id==node_one', 'node.labels.zone==demo'], }, Networks: [{ Target: 'network_demo' }] }, EndpointSpec: { Ports: [] } } };
  const changed = serviceArgs(materials, service, { image: 'iquops/demo:run_two' }, existing);
  assert.ok(changed.indexOf('--constraint-add') < changed.indexOf('--constraint-rm'));
  assert.ok(changed.includes('node.id==node_two'));
  assert.ok(changed.includes('node.id==node_one'));
  assert.ok(changed.includes('--constraint-rm') && changed.includes('node.id==node_one'));

  const unchanged = serviceArgs(materials, { ...service, nodeId: 'node_one' }, { image: 'iquops/demo:run_two' }, existing);
  assert.equal(unchanged.includes('--constraint-add'), false);
  assert.equal(unchanged.includes('--constraint-rm'), false);
});
test('更新服务时清除历史流量权重标签，入口独立维护比例', () => {
  const materials = { applicationId: 'app_demo', network: 'iquops-public', networkId: 'network_demo', containerPort: 3000 };
  const service = { serviceName: 'demo-one', slotKey: 'one', nodeId: 'node_one', replicas: 1, publishedPort: 0, environment: {} };
  const existing = { Spec: { Labels: { 'iquops.access-weight': '0' }, TaskTemplate: { ContainerSpec: {}, Placement: {}, Networks: [{ Target: 'network_demo' }] } } };
  const args = serviceArgs(materials, service, { image: 'iquops/demo:run_two' }, existing);
  assert.ok(args.includes('traefik.enable=true'));
  assert.equal(args[args.indexOf('--label-rm') + 1], 'iquops.access-weight');
});
test('健康检查固定使用网关已验证的镜像摘要', () => {
  assert.equal(healthProbeImage, 'curlimages/curl@sha256:3fb7e13e5d179a845c97d78204f3f1064d1907037f26a3ebf91710571151fd93');
  assert.ok(!healthProbeImage.includes('8.12.1'));
});
test('控制token长度与内容均必须匹配', () => {
  assert.equal(sameSecret('a'.repeat(64), 'a'.repeat(64)), true);
  assert.equal(sameSecret('a'.repeat(64), 'b'.repeat(64)), false);
  assert.equal(sameSecret('', ''), false);
  assert.equal(sameSecret('a', 'a'.repeat(64)), false);
});
test('可信库控制调用绑定真实Jenkins运行身份且禁止任意endpoint', async () => {
  const source = await readFile(new URL('../../resources/runtime/managed/jenkins/library/vars/iquopsPipeline.groovy', import.meta.url), 'utf8');
  assert.match(source, /CpsThread\.current\(\)\?\.execution/);
  assert.match(source, /execution\?\.owner\?\.executable/);
  assert.match(source, /build instanceof WorkflowRun/);
  assert.match(source, /build.parent.fullName != 'iquops\/' \+ value/);
  assert.match(source, /def runId = releaseRunId\(\)/);
  assert.ok(!source.includes('releaseRunId(currentBuild'));
  assert.match(source, /action in \['start', 'status'\]/);
  assert.match(source, /!\(value instanceof String\)/);
  assert.ok(!source.includes('IQUOPS_RUN_TOKEN'));
});

test('持久运行状态只包含非秘密字段并保留清理材料', async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'iquops-state-'));
  const file = path.join(directory, 'run.json');
  const run = {
    runId: 'run_secret_test', applicationId: 'app_demo', releasePlanId: 'plan_demo', status: 'running',
    materials: { source: { gitToken: 'sentinel-git-token' }, targets: [{ key: 'sentinel-private-key' }], services: [{ environment: { DB_PASSWORD: 'sentinel-database-password' } }] },
    callback: { token: 'sentinel-callback-token' }, pipeline: { buildCommand: 'sentinel-pipeline-command' },
    cleanup: { pending: true, network: 'iquops-build-run_secret_test', firewallRules: [['DOCKER-USER', '-s', '198.51.100.0/24', '-j', 'REJECT']] },
  };
  try {
    await persistRunState(file, run);
    const raw = await readFile(file, 'utf8');
    for (const sentinel of ['sentinel-git-token', 'sentinel-private-key', 'sentinel-database-password', 'sentinel-callback-token', 'sentinel-pipeline-command']) assert.ok(!raw.includes(sentinel));
    for (const key of ['materials', 'callback', 'pipeline']) assert.ok(!JSON.parse(raw).hasOwnProperty(key));
    const restored = restoreRunState(JSON.parse(raw), run.runId);
    assert.equal(restored.cleanup.network, 'iquops-build-run_secret_test');
    assert.equal(restored.cleanup.firewallRules.length, 1);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test('网络未移除时保持防火墙规则并拒绝密钥清理', async () => {
  const calls = [];
  let listings = 0;
  const run = { runId: 'run_cleanup', network: 'iquops-build-run_cleanup', firewallRules: [['DOCKER-USER', '-s', '198.51.100.0/24', '-j', 'REJECT']] };
  await assert.rejects(cleanupIsolation(run, {
    docker: async args => {
      calls.push(args.join(' '));
      if (args[0] === 'ps') return '';
      if (args[0] === 'network' && args[1] === 'ls') return listings++ === 0 ? run.network : run.network;
      if (args[0] === 'network' && args[1] === 'rm') throw new Error('network is still attached');
      throw new Error('unexpected docker command');
    },
    command: async (...args) => { calls.push(args.join(' ')); },
    removeFiles: async () => { calls.push('remove-files'); },
  }), /JENKINS_CLEANUP_REQUIRED/);
  assert.ok(!calls.some(call => call.startsWith('iptables')));
  assert.ok(!calls.includes('remove-files'));
  assert.equal(run.network, 'iquops-build-run_cleanup');
});

test('损坏或缺失的状态文件仍恢复限定运行的清理信息', async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'iquops-recovery-'));
  const file = path.join(directory, 'run.json');
  try {
    for (const contents of [undefined, '{"runId":', '{"runId":"another"}']) {
      if (contents !== undefined) await writeFile(file, contents);
      const recovered = await recoverRunState(file, 'jrun_recover');
      assert.equal(recovered.runId, 'jrun_recover');
      assert.equal(recovered.network, 'iquops-build-jrun_recover');
      assert.equal(recovered.cleanup.pending, true);
      assert.equal(recovered.status, 'cleanup_required');
    }
    await assert.rejects(recoverRunState(file, '../escape'), /STATE_INVALID/);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test('清理先确认容器和网络已移除，再撤规则和材料', async () => {
  const calls = [];
  let listings = 0;
  let network = 'iquops-build-run_cleanup';
  const run = { runId: 'run_cleanup', network, firewallRules: [['DOCKER-USER', '-s', '198.51.100.0/24', '-j', 'REJECT']] };
  await cleanupIsolation(run, {
    docker: async args => {
      calls.push(args.slice(0, 2).join(' '));
      if (args[0] === 'ps') return listings++ ? '' : 'abcdef123456';
      if (args[0] === 'rm') return '';
      if (args[0] === 'network' && args[1] === 'ls') return network;
      if (args[0] === 'network' && args[1] === 'rm') { network = ''; return ''; }
      throw new Error('unexpected docker command');
    },
    command: async (binary, args) => { calls.push(binary + ' ' + args[1]); },
    removeFiles: async () => { calls.push('remove-files'); },
  });
  assert.ok(calls.indexOf('rm -f') < calls.indexOf('network rm'));
  assert.ok(calls.lastIndexOf('network ls') < calls.indexOf('iptables -D'));
  assert.ok(calls.indexOf('iptables -D') < calls.indexOf('remove-files'));
  assert.equal(run.cleanup.pending, false);
});

test('容器移除失败不得放开隔离或标记清理完成', async () => {
  const run = { runId: 'run_cleanup', network: 'iquops-build-run_cleanup' };
  const calls = [];
  await assert.rejects(cleanupIsolation(run, {
    docker: async args => {
      calls.push(args[0]);
      if (args[0] === 'ps') return 'abcdef123456';
      throw new Error('docker unavailable');
    },
    command: async () => { calls.push('iptables'); },
    removeFiles: async () => { calls.push('remove-files'); },
  }), /CLEANUP_REQUIRED/);
  assert.deepEqual(calls, ['ps', 'rm']);
  assert.equal(run.network, 'iquops-build-run_cleanup');
});

test('停止Jenkins失败或构建仍在运行时不得确认取消', async () => {
  const run = { runId: 'jrun_stop', jobPath: '/job/iquops/job/jrun_stop', buildNumber: 1 };
  await assert.rejects(stopRunJob({}, run, async () => new Response('{}', { status: 403 })), /CANCEL_NOT_CONFIRMED/);
  const calls = [];
  await assert.rejects(stopRunJob({}, run, async (_, endpoint) => {
    calls.push(endpoint);
    return new Response(JSON.stringify({ building: true }), { status: 200 });
  }, async () => {}), /CANCEL_NOT_CONFIRMED/);
  assert.equal(calls.length, 11);
});

test('取消队列转运行时必须停止并确认真实构建', async () => {
  const run = { runId: 'jrun_stop', jobPath: '/job/iquops/job/jrun_stop', queueUrl: 'http://127.0.0.1:58080/queue/item/7/' };
  const calls = [];
  await stopRunJob({ jenkinsPort: 58080 }, run, async (_, endpoint) => {
    calls.push(endpoint);
    const data = endpoint === '/queue/item/7/api/json' ? { executable: { number: 3 } } : { building: false };
    return new Response(JSON.stringify(data), { status: 200 });
  });
  assert.equal(run.buildNumber, 3);
  assert.ok(calls.includes('/job/iquops/job/jrun_stop/3/stop'));
  assert.equal(calls.at(-1), '/job/iquops/job/jrun_stop/3/api/json');
});

test('队列取消后没有可识别构建时不得停止其他构建', async () => {
  const run = { runId: 'jrun_queue', jobPath: '/job/iquops/job/jrun_queue', queueUrl: 'http://127.0.0.1:58080/queue/item/8/' };
  const calls = [];
  await assert.rejects(stopRunJob({ jenkinsPort: 58080 }, run, async (_, endpoint) => {
    calls.push(endpoint);
    return new Response('', { status: endpoint.startsWith('/queue/item') ? 404 : 404 });
  }), /CANCEL_NOT_CONFIRMED/);
  assert.equal(run.buildNumber, undefined);
  assert.equal(calls.some(endpoint => endpoint.includes('/stop')), false);
});

test('没有持久状态仍可按注释回收当前运行规则', async () => {
  const deleted = [];
  await cleanupIsolation({ runId: 'jrun_lost', firewallRules: [] }, {
    docker: async () => '',
    command: async (_, args) => {
      if (args[1] === '-S') return '-A INPUT -s 192.0.2.0/24 -m comment --comment "iquops.release-run=jrun_lost" -j REJECT\n-A INPUT -s 198.51.100.0/24 -m comment --comment "iquops.release-run=jrun_other" -j REJECT';
      if (args[1] === '-D') deleted.push(args);
      return '';
    },
    removeFiles: async () => {},
  });
  assert.equal(deleted.length, 1);
  assert.ok(deleted[0].includes('iquops.release-run=jrun_lost'));
  assert.ok(!deleted[0].includes('iquops.release-run=jrun_other'));
});
