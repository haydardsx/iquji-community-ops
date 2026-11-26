import { readFile, writeFile, mkdir, rm, cp, readdir } from 'node:fs/promises';
import path from 'node:path';
import { command, contained, fail, jenkinsRequest, root, safeRelative, quote } from './broker-common.mjs';

export async function checkoutSource(run) {
  const source = run.materials.source;
  const url = source.repositoryUrl;
  if (typeof url !== 'string' || /[\r\n\0]/.test(url) || url.startsWith('-')) fail('JENKINS_SOURCE_INVALID');
  const isHTTPS = url.startsWith('https://');
  const isSSH = url.startsWith('ssh://') || /^git@[^:]+:/.test(url);
  if (!isHTTPS && !isSSH) fail('JENKINS_SOURCE_INVALID');
  if (isSSH) fail('JENKINS_GIT_SSH_NOT_CONFIGURED');
  const parsed = new URL(url);
  if (parsed.username || parsed.password || parsed.search || parsed.hash) fail('JENKINS_SOURCE_INVALID');
  const ref = source.ref;
  if (typeof ref !== 'string' || !ref || ref.startsWith('-') || /[\s\x00-\x1f~^:?*[\\]/.test(ref)) fail('JENKINS_SOURCE_REF_INVALID');
  const directory = path.join(root, 'runs', run.runId);
  const workspace = path.join(directory, 'source');
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const env = { ...process.env, GIT_TERMINAL_PROMPT: '0', GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null' };
  const auth = path.join(directory, 'git-auth');
  try {
    if (source.gitToken) {
      await writeFile(auth, '#!/bin/sh\ncase "$1" in *Username*) printf "%s" "x-access-token";; *) cat ' + quote(path.join(directory, 'git-token')) + ';; esac\n', { mode: 0o700 });
      await writeFile(path.join(directory, 'git-token'), source.gitToken, { mode: 0o600 });
      env.GIT_ASKPASS = auth;
    }
    const fetchRef = /^[a-f0-9]{40}$/.test(source.commitSha || '') ? source.commitSha : source.refType === 'branch' ? `refs/heads/${ref}` : source.refType === 'tag' ? `refs/tags/${ref}` : ref;
    if (source.refType === 'commit' && !/^[a-f0-9]{40}$/i.test(ref)) fail('JENKINS_SOURCE_REF_INVALID');
    try {
      await command('git', ['-c', 'core.hooksPath=/dev/null', 'init', workspace], { env, signal: run.abort.signal });
      await command('git', ['-C', workspace, 'remote', 'add', 'origin', url], { env, signal: run.abort.signal });
      await command('git', ['-C', workspace, '-c', 'core.hooksPath=/dev/null', 'fetch', '--depth=1', '--no-tags', 'origin', fetchRef], { env, signal: run.abort.signal, code: 'JENKINS_GIT_FETCH_FAILED', timeout: 30 * 1000 });
      await command('git', ['-C', workspace, '-c', 'core.hooksPath=/dev/null', 'checkout', '--detach', 'FETCH_HEAD'], { env, signal: run.abort.signal });
      run.commitSha = await command('git', ['-C', workspace, 'rev-parse', 'HEAD'], { env, signal: run.abort.signal });
    } catch (error) {
      if (run.abort.signal.aborted || !githubRepository(url)) throw error;
      await rm(workspace, { recursive: true, force: true });
      await checkoutGitHubArchive(run, source, directory, workspace);
    }
    if (!/^[a-f0-9]{40}$/.test(run.commitSha)) fail('JENKINS_COMMIT_INVALID');
    const file = await contained(workspace, safeRelative(source.jenkinsfilePath || 'Jenkinsfile'));
    const pipeline = await readFile(file, 'utf8');
    if (!pipeline.trim() || pipeline.length > 128 * 1024) fail('JENKINSFILE_INVALID');
    run.sourceDirectory = await contained(workspace, safeRelative(source.sourcePath || '.'));
    await rm(path.join(workspace, '.git'), { recursive: true, force: true });
    await command('chown', ['-R', '1000:1000', workspace], { signal: run.abort.signal });
    return pipeline;
  } finally {
    delete source.gitToken;
    await Promise.all([rm(auth, { force: true }), rm(path.join(directory, 'git-token'), { force: true })]);
  }
}

function githubRepository(value) {
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== 'https:' || parsed.hostname !== 'github.com' || parsed.search || parsed.hash) return null;
    const match = parsed.pathname.match(/^\/([^/]+)\/([^/]+?)(?:\.git)?\/$|^\/([^/]+)\/([^/]+?)(?:\.git)?$/);
    if (!match) return null;
    return { owner: match[1] || match[3], repo: match[2] || match[4] };
  } catch { return null; }
}

async function checkoutGitHubArchive(run, source, directory, workspace) {
  const repository = githubRepository(source.repositoryUrl);
  if (!repository) fail('JENKINS_GIT_FETCH_FAILED');
  const ref = source.commitSha || source.ref;
  const headers = { accept: 'application/vnd.github+json', 'user-agent': 'iquops-managed-jenkins' };
  if (source.gitToken) headers.authorization = `Bearer ${source.gitToken}`;
  const apiBase = `https://api.github.com/repos/${encodeURIComponent(repository.owner)}/${encodeURIComponent(repository.repo)}`;
  const commitResponse = await fetch(`${apiBase}/commits/${encodeURIComponent(ref)}`, { headers, signal: run.abort.signal });
  if (!commitResponse.ok) fail('JENKINS_GITHUB_ARCHIVE_FAILED');
  const commit = await commitResponse.json();
  const commitSha = commit?.sha;
  if (!/^[a-f0-9]{40}$/i.test(commitSha || '')) fail('JENKINS_COMMIT_INVALID');
  const archiveResponse = await fetch(`${apiBase}/tarball/${encodeURIComponent(ref)}`, { headers, signal: run.abort.signal });
  if (!archiveResponse.ok) fail('JENKINS_GITHUB_ARCHIVE_FAILED');
  const content = Buffer.from(await archiveResponse.arrayBuffer());
  if (content.length > 128 * 1024 * 1024) fail('JENKINS_SOURCE_TOO_LARGE');
  const archive = path.join(directory, 'source.tar.gz');
  const unpacked = path.join(directory, 'archive');
  await writeFile(archive, content, { mode: 0o600 });
  await mkdir(unpacked, { recursive: true, mode: 0o700 });
  try {
    await command('tar', ['-xzf', archive, '-C', unpacked], { signal: run.abort.signal, code: 'JENKINS_GITHUB_ARCHIVE_FAILED' });
    const entries = await readdir(unpacked, { withFileTypes: true });
    if (entries.length !== 1 || !entries[0].isDirectory()) fail('JENKINS_GITHUB_ARCHIVE_FAILED');
    await cp(path.join(unpacked, entries[0].name), workspace, { recursive: true, force: true });
  } finally {
    await rm(archive, { force: true });
    await rm(unpacked, { recursive: true, force: true });
  }
  run.commitSha = commitSha.toLowerCase();
}
const xml = value => String(value).replace(/[<>&"']/g, char => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;', "'": '&apos;' })[char]);
export async function createRunJob(config, run, pipeline) {
  run.jobPath = `/job/iquops/job/${run.runId}`;
  const body = `<flow-definition plugin="workflow-job"><description>OPS 受管隔离发布</description><keepDependencies>false</keepDependencies><properties><hudson.model.ParametersDefinitionProperty><parameterDefinitions><hudson.model.StringParameterDefinition><name>IQUOPS_RELEASE_RUN_ID</name><defaultValue>${xml(run.runId)}</defaultValue></hudson.model.StringParameterDefinition></parameterDefinitions></hudson.model.ParametersDefinitionProperty><org.jenkinsci.plugins.workflow.job.properties.DisableConcurrentBuildsJobProperty/></properties><definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition" plugin="workflow-cps"><script>${xml(pipeline)}</script><sandbox>true</sandbox></definition><triggers/><disabled>false</disabled></flow-definition>`;
  const response = await jenkinsRequest(config, `/job/iquops/createItem?name=${encodeURIComponent(run.runId)}`, { method: 'POST', headers: { 'content-type': 'application/xml' }, body });
  if (!response.ok) fail('JENKINS_JOB_CREATE_FAILED');
  const started = await jenkinsRequest(config, run.jobPath + '/buildWithParameters', { method: 'POST', headers: { 'content-type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams({ IQUOPS_RELEASE_RUN_ID: run.runId }) });
  if (started.status !== 201) fail('JENKINS_HANDOFF_FAILED');
  run.queueUrl = started.headers.get('location');
  if (!run.queueUrl || !/^\/queue\/item\/\d+\/$/.test(new URL(run.queueUrl, `http://127.0.0.1:${config.jenkinsPort}`).pathname)) fail('JENKINS_QUEUE_REFERENCE_INVALID');
}

export async function stopRunJob(config, run, request = jenkinsRequest, delay = ms => new Promise(resolve => setTimeout(resolve, ms))) {
  if (!run.buildNumber && run.queueUrl) {
    const queuePath = new URL(run.queueUrl, `http://127.0.0.1:${config.jenkinsPort}`).pathname;
    if (!/^\/queue\/item\/\d+\/$/.test(queuePath)) fail('JENKINS_QUEUE_REFERENCE_INVALID');
    const id = queuePath.split('/').filter(Boolean).at(-1);
    const cancelled = await request(config, `/queue/cancelItem?id=${id}`, { method: 'POST' });
    if (![200, 201, 202, 302].includes(cancelled.status)) fail('JENKINS_CANCEL_NOT_CONFIRMED');
    const state = await request(config, queuePath + 'api/json');
    if (state.ok) {
      const queue = await state.json();
      if (queue.executable?.number) run.buildNumber = queue.executable.number;
      else if (!queue.cancelled) fail('JENKINS_CANCEL_NOT_CONFIRMED');
    } else if (state.status !== 404) fail('JENKINS_CANCEL_NOT_CONFIRMED');
  }
  if (!run.buildNumber) return;
  const endpoint = `${run.jobPath}/${run.buildNumber}`;
  const stopped = await request(config, endpoint + '/stop', { method: 'POST' });
  if (![200, 201, 202, 302].includes(stopped.status)) fail('JENKINS_CANCEL_NOT_CONFIRMED');
  for (let attempt = 0; attempt < 10; attempt++) {
    const state = await request(config, endpoint + '/api/json');
    if (state.ok && (await state.json()).building === false) return;
    await delay(500);
  }
  fail('JENKINS_CANCEL_NOT_CONFIRMED');
}
