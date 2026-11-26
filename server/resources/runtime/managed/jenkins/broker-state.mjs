import { mkdir, writeFile, rename, rm, readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import path from 'node:path';
import { publicSummary } from './broker-common.mjs';

const text = value => typeof value === 'string' ? value : '';
const object = value => value && typeof value === 'object' && !Array.isArray(value) ? value : {};
const stringList = value => Array.isArray(value) ? value.map(text) : [];

export function persistedRunState(run) {
  const cleanup = object(run.cleanup);
  return {
    schemaVersion: 1,
    runId: text(run.runId),
    applicationId: text(run.applicationId),
    releasePlanId: text(run.releasePlanId),
    status: text(run.status),
    stage: text(run.stage),
    summary: publicSummary(run),
    commitSha: text(run.commitSha),
    imageDigest: text(run.imageDigest),
    artifacts: (Array.isArray(run.artifacts) ? run.artifacts : []).map(item => Object.fromEntries(
      ['serviceSlotKey', 'serverId', 'platform', 'image', 'imageDigest', 'configDigest', 'manifestDigest', ...(['registryImage', 'registryDigest'].filter(key => text(item[key])))].map(key => [key, text(item[key])])
    )),
    outputValues: run.status === 'succeeded' ? run.outputValues : undefined,
    jobPath: text(run.jobPath),
    buildNumber: Number.isInteger(run.buildNumber) ? run.buildNumber : 0,
    queueUrl: text(run.queueUrl),
    expiresAt: text(run.expiresAt),
    createdAt: text(run.createdAt),
    cleanup: {
      pending: cleanup.pending === true,
      network: text(cleanup.network || run.network),
      firewallRules: (Array.isArray(cleanup.firewallRules) ? cleanup.firewallRules : Array.isArray(run.firewallRules) ? run.firewallRules : []).map(rule => stringList(rule)),
    },
  };
}

export async function persistRunState(file, run) {
  await mkdir(path.dirname(file), { recursive: true, mode: 0o700 });
  const temporary = file + '.' + randomUUID() + '.tmp';
  try {
    await writeFile(temporary, JSON.stringify(persistedRunState(run)), { mode: 0o600, flag: 'wx' });
    await rename(temporary, file);
  } finally { await rm(temporary, { force: true }); }
}

// 状态丢失仍按目录中的运行标识回收容器、网络及临时凭据。
export async function recoverRunState(file, expectedRunId) {
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$/.test(expectedRunId)) throw new Error('JENKINS_RUN_STATE_INVALID');
  try { return restoreRunState(JSON.parse(await readFile(file, 'utf8')), expectedRunId); }
  catch {
    return restoreRunState({ runId: expectedRunId, status: 'cleanup_required', summary: 'JENKINS_RUN_STATE_INVALID', cleanup: { pending: true, network: `iquops-build-${expectedRunId}`, firewallRules: [] } }, expectedRunId);
  }
}

export function restoreRunState(value, expectedRunId) {
  const prior = object(value);
  if (prior.runId !== expectedRunId) throw new Error('JENKINS_RUN_STATE_INVALID');
  const cleanup = object(prior.cleanup);
  const legacyCleanup = {
    pending: Boolean(prior.network || prior.firewallRules?.length),
    network: prior.network,
    firewallRules: prior.firewallRules,
  };
  const run = persistedRunState({ ...prior, cleanup: Object.keys(cleanup).length ? cleanup : legacyCleanup });
  if (!run.runId) throw new Error('JENKINS_RUN_STATE_INVALID');
  run.network = run.cleanup.network;
  run.firewallRules = run.cleanup.firewallRules;
  return run;
}
