import { test } from 'node:test';
import assert from 'node:assert/strict';
import { detectBrokerFirewall } from '../../resources/runtime/managed/jenkins/broker-firewall.mjs';

const hooked = '-P FORWARD DROP\n-A FORWARD -j DOCKER-USER\n-A FORWARD -j DOCKER-FORWARD';
const firewallError = { message: 'JENKINS_BROKER_FIREWALL_UNAVAILABLE' };

function probe({ driver = 'iptables', chains = {}, socket = true, missingChain }) {
  return detectBrokerFirewall(async (binary, args) => {
    if (binary === 'docker') {
      assert.deepEqual(args.slice(0, 3), ['--host', 'unix:///var/run/docker.sock', 'info']);
      if (!socket) throw new Error('无法连接套接字');
      return driver;
    }
    assert.deepEqual(args.slice(0, 3), ['-w', '5', '-S']);
    if (!Object.hasOwn(chains, binary) || args[3] === missingChain) throw new Error('规则链不存在');
    return args[3] === 'FORWARD' ? chains[binary] : '';
  });
}

test('按实际挂接选择 legacy 与 nft 兼容层，支持没有后端字段的旧 daemon', async () => {
  for (const driver of ['iptables', '']) {
    for (const binary of ['iptables-legacy', 'iptables-nft']) {
      assert.equal(await probe({ driver, chains: { [binary]: hooked } }), binary);
    }
  }
  assert.equal(await probe({ chains: { 'iptables-nft': '-P FORWARD ACCEPT', 'iptables-legacy': hooked } }), 'iptables-legacy');
});

test('拒绝原生 nftables、双后端、未接入或会被绕过的隔离链', async () => {
  await assert.rejects(probe({ driver: 'nftables', chains: { 'iptables-legacy': hooked } }), firewallError);
  await assert.rejects(probe({ chains: { 'iptables-legacy': hooked, 'iptables-nft': hooked } }), firewallError);
  for (const forward of ['', '-A FORWARD -j ACCEPT\n-A FORWARD -j DOCKER-USER', '-A FORWARD -i docker0 -j DOCKER-USER']) {
    await assert.rejects(probe({ chains: { 'iptables-nft': forward } }), firewallError);
  }
  for (const missingChain of ['INPUT', 'DOCKER-USER', 'FORWARD']) {
    await assert.rejects(probe({ chains: { 'iptables-nft': hooked }, missingChain }), firewallError);
  }
});

test('套接字失败返回单独的稳定诊断', async () => {
  await assert.rejects(probe({ socket: false }), { message: 'JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE' });
});
