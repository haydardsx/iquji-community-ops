import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { command, fail, root } from './broker-common.mjs';
import { buildImages, executeAgent } from './broker-isolation.mjs';
import { readBuildOutputs } from './broker-variables.mjs';

function sortedEntries(values) {
  return Object.keys(values).sort().map(key => [key, values[key]]);
}

// 公开值与秘密值分别比较。组编号只在本次发布内用于命名，不包含材料及其摘要。
export function groupBuildInputs(materials) {
  const groups = new Map();
  for (const service of materials.services) {
    const buildValues = service.buildValues ?? materials.buildValues ?? {};
    const buildSecrets = service.buildSecrets ?? materials.buildSecrets ?? {};
    const key = JSON.stringify([sortedEntries(buildValues), sortedEntries(buildSecrets)]);
    if (!groups.has(key)) groups.set(key, { buildValues, buildSecrets, services: [] });
    groups.get(key).services.push(service);
  }
  return [...groups.values()];
}

export async function buildServiceGroups(run, pipeline, notify, stageLog) {
  const groups = groupBuildInputs(run.materials);
  const images = [];
  let outputs;
  for (const [index, group] of groups.entries()) {
    const buildDirectory = path.join(root, 'runs', run.runId, 'groups', String(index));
    await mkdir(buildDirectory, { mode: 0o700, recursive: true });
    let sourceDirectory = run.sourceDirectory;
    if (groups.length > 1) {
      sourceDirectory = path.join(buildDirectory, 'source');
      // 每组都从同一次检出的原始源码开始，独立拥有安装目录和编译输出。
      await command('cp', ['-a', '--', run.sourceDirectory, sourceDirectory], { signal: run.abort.signal });
    }
    const scoped = { ...run, buildDirectory, sourceDirectory, materials: { ...run.materials, ...group } };
    await executeAgent(scoped, pipeline, notify, (stage, message) => {
      run.stage = stage;
      stageLog(stage, message);
    });
    const current = await readBuildOutputs(scoped);
    if (outputs && JSON.stringify(sortedEntries(outputs)) !== JSON.stringify(sortedEntries(current))) fail('JENKINS_BUILD_OUTPUT_CONFLICT');
    outputs = current;
    run.stage = 'image';
    images.push(...await buildImages(scoped, pipeline, notify, index));
  }
  run.outputValues = outputs || {};
  run.imageDigest = [...new Set(images.map(image => image.digest))].join(',');
  return images;
}
