import {secretLogFilter} from './broker-variables.mjs';
import { randomUUID } from 'node:crypto';
import http from 'node:http';
import { restoreAccess, changeAccess } from './broker-access.mjs';
import { readFile, writeFile, readdir, mkdir } from 'node:fs/promises';
import path from 'node:path';
import { readConfig, root, sameSecret, validID, terminal, fail, publicRun, publicDiagnostic, publicSummary, validatePipeline, jenkinsRequest } from './broker-common.mjs';
import { checkoutSource, createRunJob, stopRunJob } from './broker-source.mjs';
import { prepareIsolation, cleanupIsolation } from './broker-isolation.mjs';
import { buildServiceGroups } from './broker-build-groups.mjs';
import { deployImages } from './broker-deploy.mjs';
import { persistRunState, recoverRunState } from './broker-state.mjs';

const config = await readConfig();
const runs = new Map();
const statePath = run => path.join(root, 'runs', run.runId, 'run.json');
async function persist(run) {
  await persistRunState(statePath(run), run);
}
function log(run, message) {
  run.logs.push({ index: run.nextLog++, message: secretLogFilter(run.materials)(String(message),true).slice(0, 8192) });
  while (run.logs.length > 256) run.logs.shift();
}
// 只有 broker 自身的阶段使用 OPS 事件；构建工具的原文继续按 Jenkins console 协议传递。
function stageLog(run, event, message, level = 'info', errorCode) {
  const record = { id: randomUUID(), timestamp: new Date().toISOString(), source: 'broker', event: 'broker.' + event, phase: run.stage || 'release', level, message, targetKind: 'application', targetId: run.applicationId };
  if (errorCode) record.errorCode = errorCode;
  log(run, JSON.stringify(record));
}
async function callback(run, status, summary) {
  try { await cleanupIsolation(run); } catch {
    run.status = 'cleanup_required'; run.summary = 'JENKINS_CLEANUP_REQUIRED'; run.finalizing = false; await persist(run); throw new Error('JENKINS_CLEANUP_REQUIRED');
  }
  run.status = status;
  run.summary = publicSummary({ status, summary });
  const callback = run.callback;
  if (callback) {
    const origin = new URL(callback.url);
    if (origin.hostname !== '127.0.0.1' || origin.protocol !== 'http:') fail('JENKINS_CALLBACK_URL_INVALID');
    const body = { runId: run.runId, releasePlanId: run.releasePlanId, applicationId: run.applicationId, status, summary: run.summary, outputValues:status==="succeeded"?run.outputValues:undefined, commitSha: run.commitSha, imageDigest: run.imageDigest, artifacts: status === 'succeeded' ? run.artifacts : undefined, jenkinsRun: { runId: run.runId, status, runNumber: run.buildNumber || 0, jobPath: run.jobPath || '', runUrl: '', stageUrl: '', consoleUrl: '' } };
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        const response = await fetch(callback.url, { method: 'POST', headers: { 'content-type': 'application/json', 'x-iquops-jenkins-run-token': callback.token }, body: JSON.stringify(body), signal: AbortSignal.timeout(10000) });
        if (response.ok || response.status === 401 || response.status === 409) break;
      } catch { /* Worker 会通过受认证状态查询回收丢失的回调。 */ }
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
  }
  delete run.callback; delete run.materials; delete run.pipeline;
  await persist(run);
}
async function cancel(run, { stopJob = true } = {}) {
  run.abort?.abort();
  if (run.preparation) await run.preparation.catch(() => {});
  if (run.handoff) await run.handoff.catch(() => {});
  // 等正在执行的步骤退出，避免清理后又创建新的容器或写入部署。
  if (run.execution) await run.execution;
  let stopError;
  if (stopJob) {
    try { await stopRunJob(config, run); } catch (error) { stopError = error; }
  }
  try { await cleanupIsolation(run); } catch (error) {
    run.status = 'cleanup_required'; run.summary = 'JENKINS_CLEANUP_REQUIRED'; run.finalizing = false; await persist(run); throw error;
  }
  if (stopError) {
    run.cleanup.pending = true;
    run.status = 'cleanup_required'; run.summary = 'JENKINS_CANCEL_NOT_CONFIRMED'; run.finalizing = false; await persist(run); throw stopError;
  }
}
async function execute(run, pipeline) {
  let completed = false;
  try {
    await prepareIsolation(run, () => persist(run));
    const images = await buildServiceGroups(run, pipeline, message => log(run, message), (event, message) => stageLog(run, event, message));
    await deployImages(run, images, pipeline, (message, event, level) => stageLog(run, event, message, level));
    completed = true;
  } catch (error) {
    if (run.finalizing || !run.materials) return;
    run.status = run.abort.signal.aborted ? 'cancelled' : 'build_failed';
    run.summary = publicDiagnostic(error?.message);
    stageLog(run, 'release_failed', 'Jenkins 发布未完成', run.status === 'cancelled' ? 'warn' : 'error', run.summary);
  } finally {
    try {
      await cleanupIsolation(run);
      if (completed && run.materials && !run.finalizing) { run.status = 'deployed'; run.stage = 'completed'; }
    } catch { run.status = 'cleanup_required'; run.summary = 'JENKINS_CLEANUP_REQUIRED'; run.finalizing = false; }
    if (run.materials && !run.finalizing) await persist(run);
  }
}
async function monitor(run) {
  if (run.pending || run.finalizing || !run.materials) return;
  if (Date.now() > Date.parse(run.expiresAt)) {
    run.finalizing = true;
    await cancel(run); await callback(run, 'expired', 'JENKINS_RELEASE_EXPIRED'); return;
  }
  if (!run.buildNumber && run.queueUrl) {
    const queuePath = new URL(run.queueUrl, `http://127.0.0.1:${config.jenkinsPort}`).pathname;
    const response = await jenkinsRequest(config, queuePath + 'api/json');
    if (response.ok) {
      const queue = await response.json();
      if (queue.cancelled) { run.finalizing = true; await callback(run, 'cancelled', 'JENKINS_RELEASE_CANCELLED'); return; }
      if (queue.executable?.number) { run.buildNumber = queue.executable.number; await persist(run); }
    }
  }
  if (!run.buildNumber) return;
  const response = await jenkinsRequest(config, `${run.jobPath}/${run.buildNumber}/api/json`);
  if (!response.ok) return;
  const build = await response.json();
  if (build.building || !build.result) return;
  run.finalizing = true;
  if (build.result === 'SUCCESS' && run.status === 'deployed') await callback(run, 'succeeded', '源码测试、镜像构建和指定节点部署已完成');
  else {
    await cancel(run, { stopJob: false });
    await callback(run, build.result === 'ABORTED' ? 'cancelled' : 'failed', run.summary || 'JENKINS_PIPELINE_DID_NOT_DEPLOY');
  }
}
async function body(request) {
  const chunks = []; let size = 0;
  for await (const chunk of request) { size += chunk.length; if (size > 4 * 1024 * 1024) fail('JENKINS_REQUEST_TOO_LARGE'); chunks.push(chunk); }
  return JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}');
}
const send = (response, status, value) => { response.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store' }); response.end(JSON.stringify(value)); };
async function handle(request, response) {
  const url = new URL(request.url, 'http://127.0.0.1');
  const admin = url.pathname.startsWith('/admin/');
  const expected = admin ? config.brokerToken : config.controllerToken;
  if (!sameSecret(request.headers.authorization?.replace(/^Bearer /, ''), expected)) { send(response, 401, { error: 'JENKINS_BROKER_UNAUTHORIZED' }); return; }
  const access = /^\/admin\/(access-open|access-close|access-viewer)\/([A-Za-z0-9_-]+)$/.exec(url.pathname);
  if (access && request.method === 'POST') {
    send(response, 200, await changeAccess(config, access[1], access[2], await body(request)));
    return;
  }
  if (url.pathname === '/admin/health' && request.method === 'GET') {
    const result = await jenkinsRequest(config, '/api/json?tree=numExecutors,useSecurity');
    const value = result.ok ? await result.json() : {};
    if (value.numExecutors !== 0 || value.useSecurity !== true) fail('JENKINS_SECURITY_NOT_READY');
    const folder = await jenkinsRequest(config, '/job/iquops/api/json');
    if (!folder.ok) fail('JENKINS_FOLDER_NOT_READY');
    const marker = JSON.parse(await readFile(path.join(root, 'home', 'iquops-runtime-health.json'), 'utf8'));
    if (marker.initialized !== true || marker.security !== true || marker.authorization !== true || marker.executors !== 0 || marker.folder !== 'iquops' || marker.trustedLibrary !== 'iquops' || marker.repositoryPath !== 'file:///opt/iquops/library' || marker.defaultVersion !== 'main' || marker.allowVersionOverride !== false || marker.sandbox !== true) fail('JENKINS_SECURITY_NOT_READY');
    send(response, 200, { ok: true, executionVersion: 1 }); return;
  }
  if (url.pathname === '/admin/register' && request.method === 'POST') {
    const input = await body(request);
    if (!validID(input.runId) || !input.materials || runs.has(input.runId) || !validID(input.materials.applicationId) || !Array.isArray(input.materials.services) || !input.materials.services.length || !Array.isArray(input.materials.targets)) fail('JENKINS_RUN_INVALID');
    const run = { runId: input.runId, applicationId: input.materials.applicationId, releasePlanId: input.materials.releasePlanId, materials: input.materials, callback: input.callback, expiresAt: input.expiresAt, status: 'queued', stage: 'checkout', createdAt: new Date().toISOString(), logs: [], nextLog: 0, cleanup: { pending: false, network: '', firewallRules: [] }, abort: new AbortController(), pending: true };
    runs.set(run.runId, run);
    await persist(run);
    try {
      run.preparation = checkoutSource(run);
      const source = await run.preparation;
      if (run.abort.signal.aborted) fail('JENKINS_RELEASE_CANCELLED');
      await writeFile(path.join(root, 'runs', run.runId, 'Jenkinsfile'), source, { mode: 0o600 });
      run.status = 'prepared';
      run.pending = false;
      await persist(run);
      send(response, 200, publicRun(run));
    } catch (error) {
      run.pending = false;
      await cancel(run);
      await callback(run, 'failed', publicDiagnostic(error?.message));
      throw error;
    }
    return;
  }
  const match = url.pathname.match(/^\/(admin|pipeline)\/(status|start|trigger|cancel|console|stages)\/([a-zA-Z0-9_-]+)$/);
  if (!match || !runs.has(match[3])) { send(response, 404, { error: 'JENKINS_RUN_NOT_FOUND' }); return; }
  const run = runs.get(match[3]); const action = match[2];
  if (action === 'trigger' && admin && request.method === 'POST') {
    if (run.status !== 'prepared' || run.finalizing) fail('JENKINS_RUN_STATE_INVALID');
    run.pending = true;
    try {
      const pipeline = await readFile(path.join(root, 'runs', run.runId, 'Jenkinsfile'), 'utf8');
      run.status = 'queued';
      run.handoff = createRunJob(config, run, pipeline);
      await run.handoff;
      await persist(run);
      send(response, 200, publicRun(run));
    } finally { run.pending = false; }
    return;
  }
  if (action === 'start' && match[1] === 'pipeline' && request.method === 'POST') {
    const pipeline = validatePipeline(await body(request));
    if (run.status !== 'queued' || run.finalizing || !run.materials || run.abort.signal.aborted) fail('JENKINS_RUN_ALREADY_STARTED');
    run.status = 'running';
    run.pipeline = pipeline;
    run.execution = execute(run, pipeline).catch(() => { run.status = 'cleanup_required'; run.summary = 'JENKINS_CLEANUP_REQUIRED'; });
    send(response, 202, publicRun(run)); return;
  }
  if (action === 'cancel' && admin && request.method === 'POST') {
    if (run.cancelPromise) await run.cancelPromise;
    else if (run.materials || run.cleanup?.pending) {
      run.finalizing = true;
      run.cancelPromise = (async () => { await cancel(run); await callback(run, 'cancelled', 'JENKINS_RELEASE_CANCELLED'); })();
      try { await run.cancelPromise; } finally { delete run.cancelPromise; }
    } else if (!terminal(run.status)) {
      // 预检或源码登记阶段失败后，持久化状态可能已经清除材料；此时没有远端任务和隔离资源，直接确认终态。
      run.status = 'cancelled';
      run.summary = run.summary || 'JENKINS_RELEASE_CANCELLED';
      await persist(run);
    }
    send(response, 200, publicRun(run)); return;
  }
  if (action === 'status' && request.method === 'GET') {
    send(response, 200, { ...publicRun(run), artifacts: admin && run.status === 'succeeded' ? run.artifacts : undefined, outputValues: admin && run.status==="succeeded" ? run.outputValues : undefined, logs: admin ? undefined : run.logs.filter(item => item.index >= Number(url.searchParams.get('after') || 0)) }); return;
  }
  if (admin && ['console', 'stages'].includes(action) && request.method === 'GET' && run.buildNumber) {
    const suffix = action === 'console' ? 'consoleText' : 'wfapi/describe';
    const result = await jenkinsRequest(config, `${run.jobPath}/${run.buildNumber}/${suffix}`);
    const text = (await result.text()).slice(-256 * 1024);
    send(response, result.ok ? 200 : 502, { content: text, contentType: action === 'console' ? 'text/plain' : 'application/json' }); return;
  }
  send(response, 405, { error: 'METHOD_NOT_ALLOWED' });
}
await mkdir(path.join(root, 'runs'), { recursive: true, mode: 0o700 });
for (const entry of await readdir(path.join(root, 'runs'), { withFileTypes: true })) {
  if (!entry.isDirectory() || !validID(entry.name)) continue;
  const run = await recoverRunState(path.join(root, 'runs', entry.name, 'run.json'), entry.name);
  run.abort = new AbortController(); run.logs = []; run.nextLog = 0;
  run.network ||= ['iquops-build-', entry.name].join('');
  run.cleanup.pending = true;
  run.cleanup.network = run.network;
  run.network = run.cleanup.network;
  run.firewallRules = run.cleanup.firewallRules;
  runs.set(run.runId, run);
  await persist(run);
  try {
    await cleanupIsolation(run);
    if (!terminal(run.status)) { run.status = 'failed'; run.summary = 'JENKINS_BROKER_RECOVERY_CANCELLED'; }
    await persist(run);
  } catch {
    run.status = 'cleanup_required'; run.summary = 'JENKINS_CLEANUP_REQUIRED';
    await persist(run);
    throw new Error('JENKINS_RECOVERY_CLEANUP_REQUIRED');
  }
}
await restoreAccess(config);
http.createServer((request, response) => handle(request, response).catch(error => send(response, 400, { error: publicDiagnostic(error?.message, 'JENKINS_BROKER_REQUEST_FAILED') }))).listen(config.brokerPort, '127.0.0.1');
setInterval(() => {
  for (const run of runs.values()) {
    if (run.monitoring) continue;
    run.monitoring = true;
    void monitor(run).catch(() => {}).finally(() => { run.monitoring = false; });
  }
}, 2000);
