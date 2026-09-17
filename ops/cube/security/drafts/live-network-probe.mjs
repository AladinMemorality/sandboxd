// UNEXECUTED DRAFT: not an isolation attestation. See drafts/README.md.
// Disposable guest workload. Contains no management credentials.
import fs from 'node:fs';
import net from 'node:net';
import dns from 'node:dns/promises';
import https from 'node:https';
import dgram from 'node:dgram';
import { spawnSync } from 'node:child_process';

const config = JSON.parse(fs.readFileSync('network-probe-config.json', 'utf8'));
const tcp = (host, port, extra = {}) => new Promise(resolve => {
  let finished = false;
  const socket = net.connect({ host, port, ...extra });
  const finish = (connected, error) => {
    if (finished) return;
    finished = true; socket.destroy(); resolve({ connected, error });
  };
  socket.setTimeout(800, () => finish(false, 'timeout'));
  socket.once('connect', () => finish(true, null));
  socket.once('error', e => finish(false, e.code));
});
const pingRegistry = () => new Promise(resolve => {
  const req = https.get('https://registry.npmjs.org/-/ping', { timeout: 4000 }, res => {
    res.resume(); resolve({ succeeded: res.statusCode === 200, status: res.statusCode });
  });
  req.once('timeout', () => req.destroy(new Error('timeout')));
  req.once('error', error => resolve({ succeeded: false, error: error.code || 'request_failed' }));
});
const udp = (host, port, localPort) => new Promise(resolve => {
  const socket = dgram.createSocket('udp4');
  let timer, finished = false;
  const finish = (received, error) => {
    if (finished) return;
    finished = true; clearTimeout(timer); socket.close(); resolve({ received, error });
  };
  socket.once('error', e => finish(false, e.code));
  socket.once('message', () => finish(true, null));
  socket.bind(localPort, '0.0.0.0', () => {
    timer = setTimeout(() => finish(false, 'timeout'), 800);
    socket.send(Buffer.from('cube-isolation-sentinel'), port, host);
  });
});

async function probe() {
  const targets = await Promise.all(config.targets.map(async target => {
    let answers = [], dnsError = null;
    try { answers = await Promise.race([dns.resolve4(target.domain), new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), 3000))]); }
    catch (error) { dnsError = error.code || 'timeout'; }
    // Deliberately test the numeric address AFTER learning the permitted DNS
    // answer: a private A response must not authorize that destination.
    const direct = await tcp(target.ip, target.port);
    const rebound = await tcp(target.domain, target.port);
    return { ...target, answers, dnsError, direct, rebound };
  }));
  const raw = spawnSync('python3', ['-c', 'import socket\nsocket.socket(socket.AF_INET,socket.SOCK_RAW,socket.IPPROTO_TCP)'], { encoding: 'utf8', timeout: 2000 });
  const report = {
    timestamp: Date.now(), uid: process.getuid(), registry: await pingRegistry(), targets,
    ipv6: await tcp('2606:4700:4700::1111', 443),
    forgedUDPSourcePort: await udp(config.workerIP, config.udpPort, 3000),
    forgedTCPSourcePort: await tcp(config.workerIP, config.tcpPort, { localPort: 3000 }),
    rawPacketCreationDenied: raw.status !== 0 && /PermissionError|Operation not permitted/.test(raw.stderr || ''),
    rawPacketProbeRan: !raw.error,
  };
  fs.writeFileSync('network-probe-result.json', JSON.stringify(report));
}
await probe();
// Keeping this same process alive verifies the post-resume packet path.
setInterval(() => probe().catch(() => {}), 8000);
