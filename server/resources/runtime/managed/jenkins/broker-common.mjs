import { spawn } from 'node:child_process';
import { readFile, writeFile, mkdir, realpath } from 'node:fs/promises';
import path from 'node:path';
import { timingSafeEqual } from 'node:crypto';

export const root = '/opt/iquops/jenkins';
export const configPath = process.env.IQUOPS_CONTROL_CONFIG || '/run/iquops/control.json';
export const readConfig = async () => JSON.parse(await readFile(configPath, 'utf8'));
export const validID = value => typeof value === 'string' && /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$/.test(value);
export const terminal = status => ['succeeded', 'failed', 'cancelled', 'expired'].includes(status);
export function fail(code) { throw new Error(code); }
const diagnosticCodes = new Set([
  'APPLICATION_IMAGE_SHELL_CHECK_FAILED', 'APPLICATION_IMAGE_SHELL_UNAVAILABLE',
  'APPLICATION_INITIALIZATION_FAILED', 'APPLICATION_NODE_OWNERSHIP_MISMATCH',
  'APPLICATION_PERSISTENT_SERVICE_REQUIRES_SINGLE_NODE', 'APPLICATION_VOLUME_CREATE_FAILED',
  'APPLICATION_VOLUME_INVALID', 'APPLICATION_VOLUME_MISSING', 'APPLICATION_VOLUME_NAME_DUPLICATE',
  'APPLICATION_VOLUME_NODE_CHANGE_UNSUPPORTED', 'APPLICATION_VOLUME_OWNERSHIP_MISMATCH',
  'APPLICATION_VOLUME_QUERY_FAILED', 'APPLICATION_VOLUME_TARGET_DUPLICATE',
  'JENKINS_AUTHENTICATION_FAILED', 'JENKINS_BROKER_FAILED', 'JENKINS_BROKER_RECOVERY_CANCELLED',
  'JENKINS_BROKER_REQUEST_FAILED', 'JENKINS_BROKER_UNAUTHORIZED', 'JENKINS_BROKER_UNAVAILABLE',
  'JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE', 'JENKINS_BROKER_FIREWALL_UNAVAILABLE',
  'JENKINS_BUILD_OUTPUT_CONFLICT', 'JENKINS_CALLBACK_URL_INVALID', 'JENKINS_CANCEL_NOT_CONFIRMED',
  'JENKINS_CLEANUP_REQUIRED', 'JENKINS_CLI_INPUT_INVALID', 'JENKINS_COMMAND_FAILED',
  'JENKINS_COMMIT_INVALID', 'JENKINS_DEPLOY_CONTAINER_NOT_READY', 'JENKINS_DEPLOY_CONVERGENCE_TIMEOUT',
  'JENKINS_DEPLOY_HEALTH_NOT_READY', 'JENKINS_DEPLOY_HEALTH_PROBE_FAILED', 'JENKINS_DEPLOY_HEALTH_TIMEOUT',
  'JENKINS_DEPLOY_INSPECT_FAILED', 'JENKINS_DEPLOY_INSPECT_INVALID', 'JENKINS_DEPLOY_LOGGING_MISMATCH',
  'JENKINS_DEPLOY_PORTS_CONFLICT', 'JENKINS_DEPLOY_QUERY_FAILED', 'JENKINS_DEPLOY_ROLLED_BACK',
  'JENKINS_DEPLOY_SERVICE_FAILED', 'JENKINS_DEPLOY_SSH_FAILED', 'JENKINS_DEPLOY_TARGET_INVALID',
  'JENKINS_DEPLOY_WAIT_FAILED', 'JENKINS_DEPLOYMENT_MANAGER_REQUIRED', 'JENKINS_ENDPOINT_INVALID',
  'JENKINS_FOLDER_NOT_READY', 'JENKINS_GIT_FETCH_FAILED', 'JENKINS_GIT_SSH_NOT_CONFIGURED',
  'JENKINS_GITHUB_ARCHIVE_FAILED', 'JENKINS_HANDOFF_FAILED', 'JENKINS_IMAGE_BUILD_FAILED',
  'JENKINS_IMAGE_DELIVERY_FAILED', 'JENKINS_IMAGE_DIGEST_INVALID', 'JENKINS_IMAGE_DIGEST_MISMATCH',
  'JENKINS_IMAGE_PLATFORM_MISMATCH', 'JENKINS_ISOLATION_FIREWALL_FAILED',
  'JENKINS_ISOLATION_NETWORK_INVALID', 'JENKINS_JOB_CREATE_FAILED', 'JENKINS_NODE_OWNERSHIP_MISMATCH',
  'JENKINS_OUTPUT_FILE_INVALID', 'JENKINS_OUTPUT_INVALID', 'JENKINS_OVERLAY_REQUIRED',
  'JENKINS_PIPELINE_CONFIG_INVALID', 'JENKINS_PIPELINE_DID_NOT_DEPLOY', 'JENKINS_PROJECT_STEP_FAILED',
  'JENKINS_QUEUE_REFERENCE_INVALID', 'JENKINS_RECOVERY_CLEANUP_REQUIRED',
  'DOCKER_REGISTRY_MIRROR_DRIFT', 'JENKINS_REGISTRY_PUSH_FAILED', 'JENKINS_REGISTRY_VERIFY_FAILED', 'JENKINS_REGISTRY_MIRROR_INVALID', 'JENKINS_RELEASE_CANCELLED', 'JENKINS_RELEASE_EXPIRED',
  'JENKINS_RELEASE_FAILED', 'JENKINS_REQUEST_TOO_LARGE', 'JENKINS_ROOTLESS_BUILDER_UNAVAILABLE',
  'JENKINS_RUN_ALREADY_STARTED', 'JENKINS_RUN_INVALID', 'JENKINS_RUN_NOT_FOUND',
  'JENKINS_RUN_STATE_INVALID', 'JENKINS_SECURITY_NOT_READY', 'JENKINS_SERVICE_OWNERSHIP_MISMATCH',
  'JENKINS_SOURCE_INVALID', 'JENKINS_SOURCE_PATH_INVALID', 'JENKINS_SOURCE_REF_INVALID',
  'JENKINS_SOURCE_TOO_LARGE', 'JENKINS_SWARM_CLUSTER_MISMATCH', 'JENKINS_TARGET_PLATFORM_UNSUPPORTED',
  'JENKINS_VARIABLE_INVALID', 'JENKINSFILE_INVALID',
]);

export function publicDiagnostic(value, fallback = 'JENKINS_RELEASE_FAILED') {
  return diagnosticCodes.has(value) ? value : fallback;
}

export function publicSummary(run) {
  if (!run.summary) return '';
  if (run.status === 'succeeded') return '源码测试、镜像构建和指定节点部署已完成';
  return publicDiagnostic(run.summary);
}

export function sameSecret(a, b) {
  return typeof a === 'string' && typeof b === 'string' && a.length > 20 && a.length === b.length && timingSafeEqual(Buffer.from(a), Buffer.from(b));
}
export function safeRelative(value) {
  if (typeof value !== 'string' || !value || path.isAbsolute(value) || value.includes('\0') || value.split(/[\\/]/).includes('..')) fail('JENKINS_SOURCE_PATH_INVALID');
  return value;
}
export async function contained(base, value) {
  const target = await realpath(path.resolve(base, safeRelative(value)));
  const parent = await realpath(base);
  if (target !== parent && !target.startsWith(parent + path.sep)) fail('JENKINS_SOURCE_PATH_INVALID');
  return target;
}
export const quote = value => "'" + String(value).replaceAll("'", "'\\''") + "'";
export async function saveJSON(file, value) {
  await mkdir(path.dirname(file), { recursive: true, mode: 0o700 });
  await writeFile(file, JSON.stringify(value), { mode: 0o600 });
}
// 所有执行均使用参数数组，项目命令只进入隔离容器的 shell。
export function command(binary, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(binary, args, { env: options.env || process.env, cwd: options.cwd, stdio: ['pipe', 'pipe', 'pipe'], signal: options.signal });
    let output = '', errors = '', size = 0;
    const max = options.maxBytes || 4 * 1024 * 1024;
    const filters = [options.logFilter?.(), options.logFilter?.()];
    const capture = (chunk, stderr) => {
      size += chunk.length;
      if (size > max) { child.kill('SIGKILL'); return; }
      if (stderr) errors += chunk.toString(); else output += chunk.toString();
      const filter=filters[stderr ? 1 : 0]; const text=filter?filter(chunk.toString()):chunk.toString(); if(text)options.log?.(text);
    };
    child.stdout.on('data', chunk => capture(chunk, false));
    child.stderr.on('data', chunk => capture(chunk, true));
    let processError;
    child.on('error', error => { processError = error; });
    const timer = setTimeout(() => child.kill('SIGKILL'), options.timeout || 15 * 60 * 1000);
    child.on('close', code => {
      clearTimeout(timer);
      for(const filter of filters){const tail=filter?.("",true);if(tail)options.log?.(tail);}
      if (processError) reject(processError);
      else if (code === 0 && size <= max) resolve(output.trim());
      else reject(Object.assign(new Error(options.code || 'JENKINS_COMMAND_FAILED'), { diagnostic: errors.slice(-2000), exitCode: code }));
    });
    child.stdin.on('error', () => {});
    child.stdin.end(options.input);
  });
}
export const docker = (args, options) => command('docker', args, options);
export function validatePipeline(input) {
  const keys = ['nodeImage', 'installCommand', 'testCommand', 'buildCommand', 'dockerfile', 'context', 'healthPath'];
  if (!input || typeof input !== 'object' || Array.isArray(input) || Object.keys(input).some(key => !keys.includes(key))) fail('JENKINS_PIPELINE_CONFIG_INVALID');
  const result = { nodeImage: 'node:22-bookworm-slim', dockerfile: 'Dockerfile', context: '.', healthPath: '/health', ...input };
  for (const key of keys) if (typeof result[key] !== 'string' || !result[key] || result[key].length > 4096 || result[key].includes('\0')) fail('JENKINS_PIPELINE_CONFIG_INVALID');
  if (!/^[a-zA-Z0-9][a-zA-Z0-9._/:@-]+$/.test(result.nodeImage) || !result.healthPath.startsWith('/') || /[\r\n]/.test(result.healthPath)) fail('JENKINS_PIPELINE_CONFIG_INVALID');
  safeRelative(result.context); safeRelative(result.dockerfile);
  return result;
}
export async function jenkinsRequest(config, endpoint, options = {}) {
  const base = `http://127.0.0.1:${config.jenkinsPort}`;
  if (!endpoint.startsWith('/') || endpoint.startsWith('//')) fail('JENKINS_ENDPOINT_INVALID');
  const authorization = 'Basic ' + Buffer.from(`${config.adminUser}:${config.adminPassword}`).toString('base64');
  const headers = { authorization, ...options.headers };
  if (options.method && options.method !== 'GET') {
    const crumb = await fetch(base + '/crumbIssuer/api/json', { headers: { authorization }, signal: AbortSignal.timeout(15000) });
    if (!crumb.ok) fail('JENKINS_AUTHENTICATION_FAILED');
    const cookie = crumb.headers.get('set-cookie');
    const value = await crumb.json();
    headers[value.crumbRequestField] = value.crumb;
    if (cookie) headers.cookie = cookie.split(';')[0];
  }
  return fetch(base + endpoint, { ...options, headers, redirect: 'manual', signal: AbortSignal.timeout(30000) });
}
export function publicRun(run) {
  return { runId: run.runId, status: run.status, stage: run.stage, summary: publicSummary(run), commitSha: run.commitSha, imageDigest: run.imageDigest, jobPath: run.jobPath, buildNumber: run.buildNumber, queueUrl: run.queueUrl };
}
