import test from 'node:test';
import assert from 'node:assert/strict';
import { applicationLogPolicy, deployImages, serviceArgs } from '../../resources/runtime/managed/jenkins/broker-deploy.mjs';
import { quote } from '../../resources/runtime/managed/jenkins/broker-common.mjs';

const image = 'iquops/app_demo:run_demo';
const digest = 'sha256:' + 'a'.repeat(64);
const json = JSON.stringify;
const begins = (script, args) => script.startsWith(args.map(quote).join(' '));
const commandFailure = exitCode => Object.assign(new Error('sentinel-private-diagnostic'), { exitCode, diagnostic: 'sentinel-private-diagnostic' });

function scenario(options = {}) {
  const service = { serviceName: 'demo-one', slotKey: 'one', serverId: 'server_demo', nodeId: 'node_demo', replicas: 1, publishedPort: 0, platform: 'linux/amd64', environment: { VALUE: 'sentinel-private-value' } };
  const run = {
    runId: 'run_demo', abort: new AbortController(),
    materials: {
      applicationId: 'app_demo', deploymentManagerServerId: 'server_demo', buildServerId: 'server_demo', clusterId: 'cluster_demo', network: 'network_demo', containerPort: 3000,
      targets: [{ id: 'server_demo', user: 'root', host: '192.0.2.10', port: 22, key: 'sentinel-private-key' }], services: [service],
    },
  };
  const calls = [], sleeps = [], notices = [];
  let healthCalls = 0, clock = 0;
  const ops = {
    mkdir: async () => {}, writeFile: async () => {},
    loadImage: async () => { calls.push('load-image'); },
    now: () => clock,
    sleep: async (milliseconds, signal) => {
      assert.equal(signal, run.abort.signal);
      sleeps.push(milliseconds); clock += milliseconds;
      await options.sleep?.(run);
    },
    command: async (binary, args, commandOptions) => {
      assert.equal(binary, 'ssh');
      assert.equal(commandOptions.signal, run.abort.signal);
      const script = args.at(-1);
      calls.push(script);
      if (options.failureAt && begins(script, options.failureAt)) throw commandFailure(options.exitCode ?? 1);
      if (begins(script, ['docker', 'info'])) return options.swarm ?? json({ ControlAvailable: true, LocalNodeState: 'active', Cluster: { ID: 'cluster_demo' } });
      if (begins(script, ['docker', 'network', 'inspect'])) return options.network ?? json([{ Id: 'network_demo', Driver: 'overlay', Scope: 'swarm' }]);
      if (begins(script, ['docker', 'node', 'inspect'])) return options.node ?? json([{ Status: { State: 'ready' }, Spec: { Labels: { 'iquops.server-id': 'server_demo' } } }]);
      if (begins(script, ['docker', 'service', 'ls'])) return '';
      if (begins(script, ['docker', 'image', 'inspect'])) return options.loadedImage ?? json([{ Id: digest, Os: 'linux', Architecture: 'amd64' }]);
      if (begins(script, ['docker', 'service', 'create'])) return 'demo-one';
      if (begins(script, ['docker', 'service', 'inspect'])) return json([{ Spec: { TaskTemplate: { LogDriver: options.logDriver ?? { Name: 'json-file', Options: { 'max-size': '10m', 'max-file': '3' } } } } }]);
      if (begins(script, ['docker', 'service', 'ps'])) return 'task_demo';
      if (begins(script, ['docker', 'inspect', '--type', 'task'])) return json([{ Status: { State: 'running' }, NodeID: 'node_demo', Spec: { ContainerSpec: { Image: image } } }]);
      if (begins(script, ['docker', 'run'])) {
        healthCalls++;
        assert.ok(script.includes([quote('--output'), quote('/dev/null')].join(' ')));
        assert.ok(script.includes(quote('http://demo-one:3000/health')));
        clock += options.probeDuration || 0;
        await options.onProbe?.(run, healthCalls);
        const exitCode = options.healthExitCodes?.[healthCalls - 1] ?? options.healthExitCode ?? 0;
        if (exitCode) throw commandFailure(exitCode);
        return '';
      }
      throw new Error('unexpected command in deployment test');
    },
  };
  if (options.realSleep) delete ops.sleep;
  const execute = () => deployImages(run, [{ platform: 'linux/amd64', slots: ['one'], file: '/workspace/image.tar', image, digest, manifestDigest: options.manifestDigest }], { healthPath: '/health' }, message => notices.push(message), ops);
  return { execute, run, calls, sleeps, notices, healthCalls: () => healthCalls };
}

test('健康检查在启动抖动后成功，不重复写入部署', async () => {
  const value = scenario({ healthExitCodes: [7, 22, 0] });
  await value.execute();
  assert.equal(value.healthCalls(), 3);
  assert.deepEqual(value.sleeps, [2000, 2000]);
  assert.equal(value.calls.filter(call => begins(call, ['docker', 'service', 'create'])).length, 1);
  assert.ok(value.notices.some(message => message.includes('检查容器端口与健康路径')));
  assert.ok(value.notices.some(message => message.includes('通过健康检查')));
  assert.ok(value.notices.every(message => !message.includes('sentinel') && !message.includes('http://')));
});

test('显式无端口的应用核验副本和镜像且不创建 HTTP 探针或网关入口', async () => {
  const value = scenario();
  value.run.materials.services[0].ports = [];
  await value.execute();
  assert.equal(value.healthCalls(), 0);
  const create = value.calls.find(call => begins(call, ['docker', 'service', 'create']));
  assert.ok(create.includes('traefik.enable=false'));
  assert.ok(!create.includes('iquops.port.web'));
  assert.ok(value.calls.some(call => begins(call, ['docker', 'inspect', '--type', 'task'])));
});

test('持续未就绪最多执行三十次，并给出健康检查超时分类', async () => {
  const value = scenario({ healthExitCode: 7 });
  await assert.rejects(value.execute(), { message: 'JENKINS_DEPLOY_HEALTH_TIMEOUT' });
  assert.equal(value.healthCalls(), 30);
  assert.equal(value.sleeps.length, 29);
  assert.equal(value.notices.some(message => message.includes('通过健康检查')), false);
});

test('健康重试同时受总时间窗口限制', async () => {
  const value = scenario({ healthExitCode: 28, probeDuration: 60000 });
  await assert.rejects(value.execute(), { message: 'JENKINS_DEPLOY_HEALTH_TIMEOUT' });
  assert.equal(value.healthCalls(), 3);
  assert.equal(value.sleeps.length, 2);
});

test('仅对连接、DNS、HTTP和传输暂态错误重试', async () => {
  const failures = [6, 7, 22, 28, 52, 56];
  const value = scenario({ healthExitCodes: [...failures, 0] });
  await value.execute();
  assert.equal(value.healthCalls(), failures.length + 1);
  assert.equal(value.sleeps.length, failures.length);
});

test('取消会中断真实重试等待，且不再执行健康探针', async () => {
  const value = scenario({ realSleep: true, healthExitCode: 7, onProbe: run => { setImmediate(() => run.abort.abort()); } });
  await assert.rejects(value.execute(), { message: 'JENKINS_RELEASE_CANCELLED' });
  assert.equal(value.healthCalls(), 1);
  assert.equal(value.notices.some(message => message.includes('通过健康检查')), false);
});

test('已取消发布不读取密钥或执行远端命令', async () => {
  const value = scenario();
  value.run.abort.abort();
  await assert.rejects(value.execute(), { message: 'JENKINS_RELEASE_CANCELLED' });
  assert.deepEqual(value.calls, []);
});

test('健康探针成功返回时若已取消，仍不能报告发布成功', async () => {
  const value = scenario({ onProbe: run => { run.abort.abort(); } });
  await assert.rejects(value.execute(), { message: 'JENKINS_RELEASE_CANCELLED' });
  assert.equal(value.healthCalls(), 1);
  assert.equal(value.notices.some(message => message.includes('通过健康检查')), false);
});

test('健康检查的SSH失败立即返回分类，且不暴露远端诊断', async () => {
  const value = scenario({ healthExitCode: 255 });
  await assert.rejects(value.execute(), error => {
    assert.equal(error.message, 'JENKINS_DEPLOY_SSH_FAILED');
    assert.equal(error.diagnostic, undefined);
    assert.equal(error.cause, undefined);
    return true;
  });
  assert.equal(value.healthCalls(), 1);
  assert.deepEqual(value.sleeps, []);
});

test('探针无法执行时立即失败，不伪装成应用尚未就绪', async () => {
  for (const healthExitCode of [1, 2, 125, 126, 127]) {
    const value = scenario({ healthExitCode });
    await assert.rejects(value.execute(), { message: 'JENKINS_DEPLOY_HEALTH_PROBE_FAILED' });
    assert.equal(value.healthCalls(), 1);
    assert.deepEqual(value.sleeps, []);
  }
});

test('查询、检查和服务写入失败分别返回安全分类', async () => {
  const cases = [
    { failureAt: ['docker', 'info'], exitCode: 255, message: 'JENKINS_DEPLOY_SSH_FAILED' },
    { failureAt: ['docker', 'info'], message: 'JENKINS_DEPLOY_INSPECT_FAILED' },
    { failureAt: ['docker', 'service', 'ls'], message: 'JENKINS_DEPLOY_QUERY_FAILED' },
    { failureAt: ['docker', 'service', 'create'], message: 'JENKINS_DEPLOY_SERVICE_FAILED' },
    { swarm: 'invalid-json', message: 'JENKINS_DEPLOY_INSPECT_INVALID' },
  ];
  for (const options of cases) {
    const value = scenario(options);
    await assert.rejects(value.execute(), { message: options.message });
    assert.equal(value.healthCalls(), 0);
  }
});

test('集群、网络、节点和镜像事实不匹配时拒绝写入服务', async () => {
  const cases = [
    { swarm: json({ ControlAvailable: true, LocalNodeState: 'active', Cluster: { ID: 'cluster_other' } }), message: 'JENKINS_SWARM_CLUSTER_MISMATCH' },
    { network: json([{ Id: 'network_demo', Driver: 'bridge', Scope: 'local' }]), message: 'JENKINS_OVERLAY_REQUIRED' },
    { node: json([{ Status: { State: 'ready' }, Spec: { Labels: { 'iquops.server-id': 'server_other' } } }]), message: 'JENKINS_NODE_OWNERSHIP_MISMATCH' },
    { loadedImage: json([{ Id: 'sha256:' + 'b'.repeat(64) }]), message: 'JENKINS_IMAGE_DIGEST_MISMATCH' },
  ];
  for (const options of cases) {
    const value = scenario(options);
    await assert.rejects(value.execute(), { message: options.message });
    assert.equal(value.calls.some(call => begins(call, ['docker', 'service', 'create'])), false);
    assert.equal(value.healthCalls(), 0);
  }
});

function portArguments(ports, publishedPort = 8080) {
  const materials = { applicationId: 'app_demo', network: 'network_demo', networkId: 'network_demo', containerPort: 3000 };
  const service = { serviceName: 'demo-one', slotKey: 'one', nodeId: 'node_demo', replicas: 1, publishedPort, environment: {} };
  const existing = { Spec: { Labels: {}, TaskTemplate: { ContainerSpec: {}, Placement: { Constraints: ['node.id==node_demo'] }, Networks: [{ Target: 'network_demo' }] }, EndpointSpec: { Ports: ports } } };
  return serviceArgs(materials, service, { image }, existing);
}
const valuesAfter = (args, flag) => args.flatMap((value, index) => value === flag ? [args[index + 1]] : []);

test('清空旧覆盖恢复镜像入口和默认参数，新复合脚本单独设置解释器', () => {
  const { materials } = scenario().run;
  const service = materials.services[0];
  const existing = { Spec: { Labels: {}, TaskTemplate: { ContainerSpec: { Command: ['/bin/sh'], Args: ['-c', 'old'] } } } };
  const inherited = serviceArgs(materials, service, { image }, existing);
  assert.deepEqual(valuesAfter(inherited, '--entrypoint'), ['']);
  assert.deepEqual(valuesAfter(inherited, '--args'), ['']);
  const command = ['/bin/sh', '-c', 'echo "a b" | cat'];
  const scripted = serviceArgs({ ...materials, command, shellEntrypoint: true }, service, { image }, existing);
  assert.deepEqual(valuesAfter(scripted, '--entrypoint'), ['/bin/sh']);
  assert.deepEqual(valuesAfter(scripted, '--args'), [command.slice(1).map(quote).join(' ')]);
  const direct = serviceArgs({ ...materials, command: ['--value', 'a b', ''] }, service, { image });
  assert.equal(direct.includes('--entrypoint'), false);
  assert.deepEqual(direct.slice(-3), ['--value', 'a b', '']);
});

test('目标镜像缺少所需解释器时在创建服务前失败', async () => {
  const value = scenario({ failureAt: ['docker', 'run', '--rm', '--network', 'none'], exitCode: 127 });
  value.run.materials.initializeCommand = ['sh', '-ec', 'echo ready'];
  await assert.rejects(value.execute(), { message: 'APPLICATION_IMAGE_SHELL_UNAVAILABLE' });
  assert.ok(!value.calls.some(call => begins(call, ['docker', 'service', 'create'])));
});

test('启动解释器按实际镜像入口核验，健康和初始化命令直接执行', async () => {
  const script = ['/bin/sh', '-c', 'echo ready'];
  for (const [entrypoint, fields, probe] of [
    [['/app'], { command: script }, false],
    [null, { command: script }, true],
    [[], { command: script }, true],
    [[''], { command: script }, true],
    [['/app'], { command: script, shellEntrypoint: true }, true],
    [['/app'], { command: script, healthCommand: script }, true],
    [['/app'], { command: script, initializeCommand: script }, true],
  ]) {
    const value = scenario({
      loadedImage: json([{ Id: digest, Os: 'linux', Architecture: 'amd64', Config: { Entrypoint: entrypoint } }]),
      failureAt: ['docker', 'run', '--rm', '--network', 'none'], exitCode: 127,
    });
    Object.assign(value.run.materials, fields);
    if (probe) await assert.rejects(value.execute(), { message: 'APPLICATION_IMAGE_SHELL_UNAVAILABLE' });
    else await value.execute();
    assert.equal(value.calls.some(call => begins(call, ['docker', 'service', 'create'])), !probe);
  }
});

test('创建和更新受管服务均注入可读取且轮转受限的应用日志驱动', () => {
  const created = serviceArgs({ applicationId: 'app_demo', network: 'network_demo', containerPort: 3000 }, { serviceName: 'demo-one', slotKey: 'one', nodeId: 'node_demo', replicas: 1, publishedPort: 0, environment: {} }, { image });
  for (const args of [created, portArguments([])]) {
    assert.deepEqual(valuesAfter(args, '--log-driver'), [applicationLogPolicy.driver]);
    assert.deepEqual(valuesAfter(args, '--log-opt'), ['max-size=10m', 'max-file=3']);
    assert.equal(args.includes('--entrypoint'), false);
    assert.equal(args.includes('--args'), false);
  }
});

test('真实服务未取得约定日志驱动和轮转设置时不宣称发布成功', async () => {
  for (const logDriver of [{ Name: 'syslog' }, { Name: 'json-file', Options: { 'max-size': '10m' } }, { Name: 'json-file', Options: { 'max-size': '20m', 'max-file': '3' } }]) {
    const value = scenario({ logDriver });
    await assert.rejects(value.execute(), { message: 'JENKINS_DEPLOY_LOGGING_MISMATCH' });
    assert.equal(value.healthCalls(), 0);
    assert.equal(value.notices.some(message => message.includes('通过健康检查')), false);
  }
});

test('相同端口映射保持不动，避免Docker删后重加去重', () => {
  const args = portArguments([{ TargetPort: 3000, PublishedPort: 8080, Protocol: 'tcp', PublishMode: 'ingress' }]);
  assert.deepEqual(valuesAfter(args, '--publish-rm'), []);
  assert.deepEqual(valuesAfter(args, '--publish-add'), []);
});

test('端口变更按目标端口、协议和模式移除，再添加新映射', () => {
  const args = portArguments([{ TargetPort: 3000, PublishedPort: 8081, Protocol: 'tcp', PublishMode: 'host' }, { TargetPort: 4000, PublishedPort: 9080, Protocol: 'udp', PublishMode: 'ingress' }]);
  assert.deepEqual(valuesAfter(args, '--publish-rm'), ['target=3000,protocol=tcp,mode=host', 'target=4000,protocol=udp,mode=ingress']);
  assert.deepEqual(valuesAfter(args, '--publish-add'), ['published=8080,target=3000']);
});

test('关闭公开端口只移除映射；相同映射与其它目标端口可分别处理', () => {
  const ports = [{ TargetPort: 3000, PublishedPort: 8080 }, { TargetPort: 4000, PublishedPort: 9080 }];
  const closed = portArguments(ports, 0);
  assert.deepEqual(valuesAfter(closed, '--publish-rm'), ['target=3000,protocol=tcp,mode=ingress', 'target=4000,protocol=tcp,mode=ingress']);
  assert.deepEqual(valuesAfter(closed, '--publish-add'), []);
  const kept = portArguments(ports);
  assert.deepEqual(valuesAfter(kept, '--publish-rm'), ['target=4000,protocol=tcp,mode=ingress']);
  assert.deepEqual(valuesAfter(kept, '--publish-add'), []);
});

test('同一删除选择器含多个发布端口时拒绝静默丢失目标映射', () => {
  assert.throws(() => portArguments([{ TargetPort: 3000, PublishedPort: 8080 }, { TargetPort: 3000, PublishedPort: 8081 }]), { message: 'JENKINS_DEPLOY_PORTS_CONFLICT' });
});

test('containerd 以清单作为镜像 ID 时同时核对构建清单和节点描述符', async () => {
  const manifestDigest = 'sha256:' + 'd'.repeat(64);
  await scenario({ manifestDigest, loadedImage: json([{ Os: 'linux', Architecture: 'amd64', Id: manifestDigest, Descriptor: { digest: manifestDigest } }]) }).execute();
  await assert.rejects(scenario({ manifestDigest, loadedImage: json([{ Os: 'linux', Architecture: 'amd64', Id: manifestDigest, Descriptor: { digest } }]) }).execute(), { message: 'JENKINS_IMAGE_DIGEST_MISMATCH' });
});

test('同一服务可分别声明 HTTP 与 SSH，持久卷单副本先停后启且失败暂停', () => {
  const materials = { applicationId: 'app_demo', network: 'overlay', containerPort: 80, volumes: [{ name: 'data', target: '/data' }] };
  const service = { serviceName: 'demo-one', slotKey: 'main', nodeId: 'node_one', replicas: 1, environment: {},
    ports: [{ name: 'http', protocol: 'http', targetPort: 3000 }, { name: 'ssh', protocol: 'tcp', targetPort: 22, publishedPort: 2222 }] };
  const args = serviceArgs(materials, service, { image });
  assert.deepEqual(valuesAfter(args, '--publish'), ['published=2222,target=22']);
  assert.ok(args.includes('iquops.port.http=http/3000') && args.includes('iquops.port.ssh=tcp/22'));
  assert.ok(args.includes('traefik.http.services.demo-one.loadbalancer.server.port=3000'));
  assert.deepEqual(valuesAfter(args, '--update-order'), ['stop-first']);
  assert.deepEqual(valuesAfter(args, '--update-failure-action'), ['pause']);
  assert.deepEqual(valuesAfter(args, '--mount'), ['type=volume,source=iquops-app_demo-data,target=/data']);
});
