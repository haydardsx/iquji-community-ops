import { readFile } from 'node:fs/promises';
import { publicDiagnostic, readConfig, validID } from './broker-common.mjs';

try {
  const config = await readConfig();
  const [action, id] = process.argv.slice(2);
  const allowed = ['register', 'trigger', 'health', 'status', 'cancel', 'console', 'stages', 'access-open', 'access-close', 'access-viewer'];
  if (!allowed.includes(action) || (!['register', 'health'].includes(action) && !validID(id))) {
    throw new Error('JENKINS_CLI_INPUT_INVALID');
  }
  const method = (['register', 'trigger', 'cancel'].includes(action) || action.startsWith('access-')) ? 'POST' : 'GET';
  const body = (action === 'register' || action.startsWith('access-')) ? await readFile('/dev/stdin', 'utf8') : method === 'POST' ? '{}' : undefined;
  const response = await fetch(`http://127.0.0.1:${config.brokerPort}/admin/${action}${id ? '/' + id : ''}`, { method, headers: { authorization: `Bearer ${config.brokerToken}`, 'content-type': 'application/json' }, body, signal: AbortSignal.timeout(action === 'register' ? 600000 : 60000) });
  const value = await response.json();
  if (!response.ok) throw new Error(value.error || 'JENKINS_BROKER_FAILED');
  process.stdout.write(JSON.stringify(value));
} catch (error) {
  process.stderr.write(publicDiagnostic(error?.message, 'JENKINS_BROKER_UNAVAILABLE'));
  process.exitCode = 1;
}
