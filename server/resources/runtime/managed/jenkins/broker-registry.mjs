import { mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { command, fail, root } from './broker-common.mjs';

const digestPattern = /^sha256:[0-9a-f]{64}$/;

async function readProgress(file) {
  try {
    const progress = JSON.parse(await readFile(file, 'utf8'));
    if (!progress || Array.isArray(progress) || typeof progress !== 'object') fail('JENKINS_REGISTRY_VERIFY_FAILED');
    return progress;
  } catch (error) {
    if (error.code === 'ENOENT') return {};
    fail('JENKINS_REGISTRY_VERIFY_FAILED');
  }
}

async function verifyImage(execute, auth, repository, reference, manifestDigest, configDigest, signal) {
  if (!digestPattern.test(manifestDigest)) fail('JENKINS_REGISTRY_VERIFY_FAILED');
  const inspect = ['inspect', '--retry-times', '2', '--authfile', auth];
  const options = {signal, code: 'JENKINS_REGISTRY_VERIFY_FAILED', timeout: 60 * 60 * 1000};
  const observed = await execute('skopeo', [...inspect, '--no-tags', '--format', '{{.Digest}}', 'docker://' + reference], options);
  if (observed !== manifestDigest) fail('JENKINS_REGISTRY_VERIFY_FAILED');
  const raw = await execute('skopeo', [...inspect, '--raw', 'docker://' + repository + '@' + manifestDigest], options);
  let manifest;
  try { manifest = JSON.parse(raw); } catch { fail('JENKINS_REGISTRY_VERIFY_FAILED'); }
  if (manifest?.config?.digest !== configDigest) fail('JENKINS_REGISTRY_VERIFY_FAILED');
}

// 只在本次运行中复用已核验标签，进度文件只包含仓库引用和内容摘要。
async function publishVariants(run, images, notify, execute, auth, progressFile) {
  const repository = run.materials.registrySync.repository;
  const progress = await readProgress(progressFile);
  const digestFile = path.join(path.dirname(auth), 'digest');
  for (const variant of images) {
    run.abort.signal.throwIfAborted();
    const tag = variant.image.slice(variant.image.lastIndexOf(':') + 1);
    const reference = repository + ':' + tag;
    const prior = progress[reference];
    let digest;
    if (prior) {
      if (prior.configDigest !== variant.digest) fail('JENKINS_REGISTRY_VERIFY_FAILED');
      digest = prior.manifestDigest;
    } else {
      await rm(digestFile, { force: true });
      await execute('skopeo', ['copy', '--retry-times', '2', '--dest-authfile', auth, '--digestfile', digestFile,
        'docker-archive:' + variant.file, 'docker://' + reference], {
        signal: run.abort.signal, code: 'JENKINS_REGISTRY_PUSH_FAILED', timeout: 60 * 60 * 1000,
      });
      digest = (await readFile(digestFile, 'utf8')).trim();
    }
    // 即使本地曾成功，重试也须按标签回读远端清单，再按摘要校验其配置。
    await verifyImage(execute, auth, repository, reference, digest, variant.digest, run.abort.signal);
    if (!prior) {
      progress[reference] = {manifestDigest: digest, configDigest: variant.digest};
      await writeFile(progressFile + '.tmp', JSON.stringify(progress), {mode: 0o600});
      await rename(progressFile + '.tmp', progressFile);
    }
    variant.registryImage = reference;
    variant.registryDigest = digest;
    notify((prior ? '已重新核验 ' : '已核验 ') + reference + '@' + digest);
  }
}

export async function syncRegistryImages(run, images, notify, execute = command, runRoot = root) {
  const registry = run.materials.registrySync;
  if (!registry) return;
  const base = path.join(runRoot, 'runs', run.runId);
  const progressFile = path.join(base, 'registry-progress.json');
  const directory = path.join(base, 'registry');
  const auth = path.join(directory, 'auth.json');
  const host = registry.repository.split('/')[0];
  const auths = registry.username ? {
    [host]: { auth: Buffer.from(registry.username + ':' + registry.password, 'utf8').toString('base64') },
  } : {};
  await mkdir(directory, { recursive: true, mode: 0o700 });
  try {
    await writeFile(auth, JSON.stringify({ auths }), { mode: 0o600 });
    run.stage = 'registry';
    notify('正在同步构建镜像并核验仓库摘要');
    try {
      await publishVariants(run, images, notify, execute, auth, progressFile);
    } catch (error) {
      if (error.message !== 'JENKINS_REGISTRY_PUSH_FAILED' || run.abort.signal.aborted) throw error;
      notify('镜像推送失败，正在复核已完成的标签并重试本次同步');
      await publishVariants(run, images, notify, execute, auth, progressFile);
    }
  } finally {
    await rm(directory, { recursive: true, force: true }).catch(() => fail('JENKINS_CLEANUP_REQUIRED'));
  }
}
