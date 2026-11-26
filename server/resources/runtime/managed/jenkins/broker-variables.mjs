import { constants } from 'node:fs';
import { chown, lstat, mkdir, open, realpath, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fail, root } from './broker-common.mjs';

const validKey = key => /^[A-Za-z_][A-Za-z0-9_]*$/.test(key) && key.length <= 256;
const directory = run => path.join(run.buildDirectory || path.join(root, 'runs', run.runId), 'build-secrets');
const values = run => Object.entries(run.materials?.buildValues || {});
const secrets = run => Object.entries(run.materials?.buildSecrets || {});

export async function prepareBuildSecrets(run) {
  const entries = [...values(run), ...secrets(run)];
  if (entries.some(([key, value]) => !validKey(key) || typeof value !== 'string' || value.includes('\0'))) fail('JENKINS_VARIABLE_INVALID');
  if (!secrets(run).length) return;
  await mkdir(directory(run), { mode: 0o700 });
  for (const [key, value] of secrets(run)) {
    const file = path.join(directory(run), key);
    await writeFile(file, value, { flag: 'wx', mode: 0o400 });
    await chown(file, 1000, 1000);
  }
}
// 由宿主逐文件只读挂载；材料目录不进入源码工作区或镜像构建上下文。
export function secretMounts(run) {
  return secrets(run).flatMap(([key]) => ['--mount', `type=bind,source=${directory(run)}/${key},target=/run/secrets/${key},readonly`]);
}
export const agentVariables = run => values(run).flatMap(([key, value]) => ['--env', key + '=' + value]);
export const buildkitVariables = run => [
  ...values(run).flatMap(([key, value]) => ['--opt', 'build-arg:' + key + '=' + value]),
  ...secrets(run).flatMap(([key]) => ['--secret', 'id=' + key + ',src=/run/secrets/' + key]),
];

export function secretLogFilter(materials) {
  const protectedValues = [...new Set([...(materials?.sensitiveValues || []), ...Object.values(materials?.buildSecrets || {})].filter(Boolean))].sort((a,b)=>b.length-a.length);
  let pending = '';
  return (chunk, final = false) => {
    const input = pending + chunk;
    let result = '', index = 0;
    pending = '';
    while (index < input.length) {
      const match=protectedValues.find(v=>input.startsWith(v,index));
      if(match){result+='[REDACTED]';index+=match.length;continue;}
      if(!final && protectedValues.some(v=>v.length > input.length-index && v.startsWith(input.slice(index)))){
        pending=input.slice(index);break;
      }
      result+=input[index++];
    }
    return result;
  };
}

export async function readBuildOutputs(run) {
  const keys = Object.keys(run.materials.buildOutputKeys || {});
  if (!keys.length) return {};
  const source = await realpath(run.sourceDirectory);
  const parent = path.join(source, '.iquops');
  if ((await realpath(parent)) !== parent || !(await lstat(parent)).isDirectory()) fail('JENKINS_OUTPUT_FILE_INVALID');
  const handle = await open(path.join(parent,'outputs.json'), constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const stat = await handle.stat();
    if (!stat.isFile() || stat.nlink !== 1 || stat.size > 65536) fail('JENKINS_OUTPUT_FILE_INVALID');
    const data = Buffer.alloc(65537);
    const {bytesRead}=await handle.read(data,0,data.length,0);
    if (bytesRead !== stat.size) fail('JENKINS_OUTPUT_FILE_INVALID');
    const outputs=JSON.parse(data.subarray(0,bytesRead).toString('utf8'));
    if (!outputs || Array.isArray(outputs) || typeof outputs!=='object' || Object.keys(outputs).length!==keys.length) fail('JENKINS_OUTPUT_INVALID');
    const protectedValues=[...(run.materials.sensitiveValues||[]),...Object.values(run.materials?.buildSecrets||{})].filter(Boolean);
    for(const key of keys){
      const value=outputs[key];
      if(!Object.hasOwn(outputs,key)||!validKey(key)||typeof value!=='string'||Buffer.byteLength(value)>4096||protectedValues.some(secret=>value.includes(secret))) fail('JENKINS_OUTPUT_INVALID');
    }
    return outputs;
  } finally { await handle.close(); }
}
