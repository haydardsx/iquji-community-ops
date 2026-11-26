import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp, mkdir, writeFile, rm, symlink} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {agentVariables,buildkitVariables,secretMounts,secretLogFilter,readBuildOutputs} from '../../resources/runtime/managed/jenkins/broker-variables.mjs';

test('构建值使用环境，构建凭据只通过只读文件传递',()=>{
  const run={runId:'demo',materials:{buildValues:{PROFILE:'public'},buildSecrets:{ACCESS:'example-value'}}};
  assert.deepEqual(agentVariables(run),['--env','PROFILE=public']);
  assert.ok(secretMounts(run).at(-1).endsWith('/run/secrets/ACCESS,readonly'));
  assert.ok(buildkitVariables(run).includes('id=ACCESS,src=/run/secrets/ACCESS'));
  assert.ok(![...buildkitVariables(run),...secretMounts(run)].join(' ').includes('example-value'));
});

test('不同块边界的完整和截断输出均遮蔽已知材料',()=>{
  for(let split=0;split<10;split++){
    const filter=secretLogFilter({sensitiveValues:['example-value']});
    const text='hi example-value ok';
    assert.equal(filter(text.slice(0,split))+filter(text.slice(split))+filter('',true),'hi [REDACTED] ok');
  }
});

test('只发布预先声明的公开构建输出',async t=>{
  const source=await mkdtemp(path.join(os.tmpdir(),'ops-build-'));t.after(()=>rm(source,{recursive:true,force:true}));
  const dir=path.join(source,'.iquops');await mkdir(dir);
  const file=path.join(dir,'outputs.json');const run={sourceDirectory:source,materials:{buildOutputKeys:{VERSION:'out_version'},sensitiveValues:['private-example']}};
  await writeFile(file,JSON.stringify({VERSION:'demo-v1'}));assert.deepEqual(await readBuildOutputs(run),{VERSION:'demo-v1'});
  for(const value of [{UNDECLARED:'demo'},{VERSION:'private-example'},{VERSION:'ok',OTHER:'no'}]){
    await writeFile(file,JSON.stringify(value));await assert.rejects(readBuildOutputs(run));
  }
  await rm(file);await symlink(path.join(source,'elsewhere'),file);await assert.rejects(readBuildOutputs(run));
});

test('同值槽位复用构建，覆盖值、秘密或值的类别不同时分组', async () => {
  const { groupBuildInputs } = await import('../../resources/runtime/managed/jenkins/broker-build-groups.mjs');
  const groups = groupBuildInputs({
    buildValues: { COLOR: 'blue', TARGET: 'web' }, buildSecrets: { TOKEN: 'sample' },
    services: [
      { slotKey: 'first' },
      { slotKey: 'same', buildValues: { TARGET: 'web', COLOR: 'blue' }, buildSecrets: { TOKEN: 'sample' } },
      { slotKey: 'color', buildValues: { COLOR: 'green' } },
      { slotKey: 'token', buildSecrets: { TOKEN: 'different' } },
      { slotKey: 'kind', buildValues: { TARGET: 'web', COLOR: 'blue', TOKEN: 'sample' }, buildSecrets: {} },
    ],
  });
  assert.deepEqual(groups.map(group => group.services.map(service => service.slotKey)), [['first', 'same'], ['color'], ['token'], ['kind']]);
});
