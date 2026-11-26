import tls from 'node:tls';
import net from 'node:net';
import path from 'node:path';
import { readFile, rename, rm, writeFile } from 'node:fs/promises';
import { jenkinsRequest, root, validID } from './broker-common.mjs';

// 每次绑定使用独立的客户端证书与端口，改绑时保留旧转发直至新入口完成核验。
const listeners = new Map();
const file = path.join(root, 'access-bindings.json');
const viewer = 'iquops-viewer';

async function saved() {
  try { return JSON.parse(await readFile(file, 'utf8')); }
  catch (error) { if (error.code === 'ENOENT') return {}; throw error; }
}
async function persist(bindings) {
  await writeFile(file + '.tmp', JSON.stringify(bindings), {mode: 0o600});
  await rename(file + '.tmp', file);
}
async function start(config, id, spec) {
  if (listeners.has(id)) return listeners.get(id).address().port;
  const sockets = new Set();
  const server = tls.createServer({
    ca: spec.ca, cert: spec.cert, key: spec.key, requestCert: true, rejectUnauthorized: true,
    minVersion: 'TLSv1.2', handshakeTimeout: 5000,
  }, (incoming) => {
    const upstream = net.connect(config.jenkinsPort, '127.0.0.1');
    sockets.add(incoming);
    incoming.once('close', () => { sockets.delete(incoming); upstream.destroy(); });
    upstream.once('close', () => incoming.destroy());
    incoming.on('error', () => upstream.destroy());
    upstream.on('error', () => incoming.destroy());
    incoming.pipe(upstream).pipe(incoming);
  });
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(spec.port || 0, '0.0.0.0', resolve);
  });
  listeners.set(id, server);
  server.shutdown = () => { for (const socket of sockets) socket.destroy(); server.close(); listeners.delete(id); };
  return server.address().port;
}
export async function restoreAccess(config) {
  for (const [id, spec] of Object.entries(await saved())) await start(config, id, spec);
}

// 固定的管理脚本只接收编码后的数据；回环 admin API 使用 CSRF crumb。
async function setViewer(config, password, publicUrl) {
  const encoded = Buffer.from(JSON.stringify({password, publicUrl})).toString('base64');
  const source = `
import jenkins.model.Jenkins
import jenkins.model.JenkinsLocationConfiguration
import hudson.model.User
import hudson.security.HudsonPrivateSecurityRealm
import groovy.json.JsonSlurper
def p = new JsonSlurper().parseText(new String('${encoded}'.decodeBase64(), 'UTF-8'))
def j = Jenkins.get()
if (!(j.authorizationStrategy instanceof hudson.security.ProjectMatrixAuthorizationStrategy)) throw new IllegalStateException()
def u = User.getById('iquops-viewer', false)
if (p.password) {
  j.securityRealm.createAccount('iquops-viewer', p.password as String)
} else if (u != null) {
  u.delete()
}
def location = JenkinsLocationConfiguration.get()
location.setUrl(p.publicUrl ?: null)
location.save()
println('OPS_VIEWER_UPDATED')
`;
  const result = await jenkinsRequest(config, '/scriptText', {method:'POST', body:new URLSearchParams({script:source})});
  if (!result.ok || (await result.text()).trim() !== 'OPS_VIEWER_UPDATED') throw Error('JENKINS_ACCESS_VIEWER_FAILED');
}

// 修改同一受管构建机的转发清单时串行持久化；失败后允许后续撤销。
let changes = Promise.resolve();
export function changeAccess(config, action, id, input) {
  const next = changes.then(() => applyAccess(config, action, id, input));
  changes = next.catch(() => {});
  return next;
}
async function applyAccess(config, action, id, input) {
  if (!validID(id)) throw Error('JENKINS_ACCESS_INVALID');
  const bindings = await saved();
  if (action === 'access-open') {
    if (!bindings[id]) {
      const spec = {ca: input.ca, cert:input.cert, key:input.key};
      const port = await start(config, id, spec);
      bindings[id] = {...spec, port};
      try { await persist(bindings); } catch(error) { listeners.get(id)?.shutdown(); throw error; }
    }
    return {port:bindings[id].port};
  }
  if (action === 'access-viewer') {
    await setViewer(config, input.password, input.url);
    // 实际身份验证必须允许查看受管目录，并拒绝管理控制台和作业配置。
    const headers = {authorization:'Basic '+Buffer.from(viewer+':'+input.password).toString('base64')};
    for (const [endpoint, allowed] of [['/job/iquops/api/json',true],['/script',false],['/job/iquops/config.xml',false]]) {
      const result = await fetch('http://127.0.0.1:'+config.jenkinsPort+endpoint,{headers,redirect:'manual',signal:AbortSignal.timeout(8000)});
      await result.body?.cancel();
      if (allowed ? !result.ok : result.status !== 403) throw Error('JENKINS_ACCESS_VIEWER_FAILED');
    }
    return {ok:true};
  }
  if (action === 'access-close') {
    listeners.get(id)?.shutdown();
    delete bindings[id];
    await persist(bindings);
    if (!Object.keys(bindings).length) {
      await setViewer(config, null, null);
      await rm(file,{force:true});
    }
    return {ok:true};
  }
  throw Error('JENKINS_ACCESS_INVALID');
}
