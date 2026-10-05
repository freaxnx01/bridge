#!/usr/bin/env node
/*
 * bridge-mcp-proxy -- stdio <-> Streamable-HTTP adapter for the bridge MCP server,
 * replacing mcp-remote for Claude Desktop.
 *
 * Why this exists: mcp-remote holds a long-lived connection, gives up after two
 * reconnect attempts, and then sits alive forever doing nothing. Claude Desktop never
 * respawns it, so bridge stays dead until Desktop restarts (33 times in 23 days, see
 * %LOCALAPPDATA%\bridge-mcp-watchdog\watchdog.log).
 *
 * This proxy holds NO persistent connection. Every JSON-RPC message from Desktop is
 * one HTTP POST. A network blip only fails the calls made during it; the next call
 * just works. It never gives up and never exits on errors -- only when stdin closes.
 *
 * Usage: node bridge-mcp-proxy.mjs <server-url>   (token via env AUTH_HEADER="Bearer ...")
 * Log:   %LOCALAPPDATA%\bridge-mcp-proxy\proxy.log
 *
 * Retry rules: a request is only re-sent if it provably never reached the server
 * (DNS/connect failure, 502/503), or if it is not a tools/call. A tools/call that may
 * have executed is never replayed -- bridge tools create issues and push files.
 *
 * If you hit a blocker with this proxy, fix it here and update this header so the
 * next run does not have to re-derive it.
 */
import { appendFileSync, mkdirSync, statSync, renameSync } from 'node:fs';
import { join } from 'node:path';
import { createInterface } from 'node:readline';

const SERVER_URL = process.argv[2] || 'https://bridge-mcp.home.freaxnx01.ch/mcp';
const AUTH = process.env.AUTH_HEADER || '';
const RETRY_BUDGET_MS = Number(process.env.BRIDGE_PROXY_RETRY_SECONDS || 60) * 1000;
const CALL_TIMEOUT_MS = Number(process.env.BRIDGE_PROXY_TIMEOUT_SECONDS || 300) * 1000;
const CONNECT_ERRORS = new Set([
  'ENOTFOUND', 'EAI_AGAIN', 'ECONNREFUSED', 'EHOSTUNREACH', 'ENETUNREACH',
  'ENETDOWN', 'UND_ERR_CONNECT_TIMEOUT',
]);

const logDir = join(process.env.LOCALAPPDATA || process.env.HOME || '.', 'bridge-mcp-proxy');
const logFile = join(logDir, 'proxy.log');
try {
  mkdirSync(logDir, { recursive: true });
  if (statSync(logFile, { throwIfNoEntry: false })?.size > 2 * 1024 * 1024) renameSync(logFile, logFile + '.1');
} catch { /* logging is best-effort */ }

function log(level, msg) {
  const line = `${new Date().toISOString()} [${level}] pid=${process.pid} ${msg}`;
  process.stderr.write(line + '\n');
  try { appendFileSync(logFile, line + '\n'); } catch { /* best-effort */ }
}

process.on('uncaughtException', (e) => log('ERROR', `uncaught: ${e?.stack || e}`));
process.on('unhandledRejection', (e) => log('ERROR', `unhandled rejection: ${e?.stack || e}`));

let sessionId = null;
let protocolVersion = null;
let initRequest = null; // replayed if the server ever forgets our session

function send(msg) {
  process.stdout.write(JSON.stringify(msg) + '\n');
}

function errorReply(id, message) {
  if (id === undefined || id === null) return;
  send({ jsonrpc: '2.0', id, error: { code: -32000, message: `bridge-mcp-proxy: ${message}` } });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function errCode(e) {
  return e?.cause?.code || e?.code || e?.name || 'UNKNOWN';
}

async function post(body) {
  const headers = {
    'Content-Type': 'application/json',
    Accept: 'application/json, text/event-stream',
  };
  if (AUTH) headers.Authorization = AUTH;
  if (sessionId) headers['Mcp-Session-Id'] = sessionId;
  if (protocolVersion) headers['MCP-Protocol-Version'] = protocolVersion;
  return fetch(SERVER_URL, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal: AbortSignal.timeout(CALL_TIMEOUT_MS),
  });
}

// Forward every JSON-RPC message in the response body to Desktop.
async function relay(res) {
  const type = res.headers.get('content-type') || '';
  if (type.includes('text/event-stream')) {
    const text = await res.text();
    for (const event of text.split(/\r?\n\r?\n/)) {
      const data = event.split(/\r?\n/).filter((l) => l.startsWith('data:')).map((l) => l.slice(5).trimStart()).join('\n');
      if (data) handleServerMessage(JSON.parse(data));
    }
  } else {
    const text = await res.text();
    if (text.trim()) handleServerMessage(JSON.parse(text));
  }
}

function handleServerMessage(msg) {
  for (const m of Array.isArray(msg) ? msg : [msg]) {
    if (m?.result?.protocolVersion && m.result.serverInfo) protocolVersion = m.result.protocolVersion;
    send(m);
  }
}

async function reinitialize() {
  if (!initRequest) return;
  log('WARN', 'server rejected session; re-initializing transparently');
  sessionId = null;
  const res = await post({ ...initRequest, id: `proxy-reinit-${Date.now()}` });
  if (res.headers.get('mcp-session-id')) sessionId = res.headers.get('mcp-session-id');
  await res.text(); // the init result is ours, not Desktop's -- don't forward it
  const ack = await post({ jsonrpc: '2.0', method: 'notifications/initialized' });
  await ack.text();
}

async function forward(msg) {
  const id = msg.id;
  const isRequest = id !== undefined && id !== null && typeof msg.method === 'string';
  const replayable = msg.method !== 'tools/call';
  if (msg.method === 'initialize') initRequest = msg;

  const deadline = Date.now() + RETRY_BUDGET_MS;
  let delay = 1000;
  let reinitDone = false;
  let attempt = 0;

  for (;;) {
    attempt++;
    let res;
    try {
      res = await post(msg);
    } catch (e) {
      const code = errCode(e);
      const neverSent = CONNECT_ERRORS.has(code);
      if ((neverSent || replayable) && Date.now() + delay < deadline) {
        log('WARN', `${msg.method ?? 'response'} attempt ${attempt} failed (${code}); retrying in ${delay / 1000}s`);
        await sleep(delay);
        delay = Math.min(delay * 2, 15000);
        continue;
      }
      log('ERROR', `${msg.method ?? 'response'} failed (${code}): ${e?.message}`);
      if (isRequest) errorReply(id, neverSent || replayable
        ? `bridge unreachable (${code}); try again in a moment`
        : `connection lost during ${msg.method} (${code}); it may or may not have run -- check before retrying`);
      return;
    }

    if (res.headers.get('mcp-session-id')) sessionId = res.headers.get('mcp-session-id');

    if (res.status === 404 && sessionId && !reinitDone && msg.method !== 'initialize') {
      await res.text();
      reinitDone = true;
      try { await reinitialize(); } catch (e) { log('ERROR', `re-initialize failed: ${e?.message}`); }
      continue;
    }

    if ((res.status === 502 || res.status === 503) && Date.now() + delay < deadline) {
      await res.text();
      log('WARN', `${msg.method ?? 'response'} got HTTP ${res.status}; retrying in ${delay / 1000}s`);
      await sleep(delay);
      delay = Math.min(delay * 2, 15000);
      continue;
    }

    if (res.status === 202 || res.status === 204) return;

    if (!res.ok) {
      const text = (await res.text()).slice(0, 300);
      log('ERROR', `${msg.method ?? 'response'} HTTP ${res.status}: ${text}`);
      if (isRequest) errorReply(id, `HTTP ${res.status} from bridge${res.status === 401 ? ' (token rejected)' : ''}`);
      return;
    }

    try {
      await relay(res);
      if (attempt > 1) log('INFO', `${msg.method} recovered after ${attempt} attempts`);
    } catch (e) {
      log('ERROR', `bad response body for ${msg.method}: ${e?.message}`);
      if (isRequest) errorReply(id, `unreadable response from bridge: ${e?.message}`);
    }
    return;
  }
}

log('INFO', `started (node ${process.version}) -> ${SERVER_URL}${AUTH ? '' : ' [WARNING: no AUTH_HEADER]'}`);

const initGate = new Set(); // in-flight initialize requests everything else waits on
const inflight = new Set();
const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
rl.on('line', (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (e) {
    log('ERROR', `unparseable line from client: ${line.slice(0, 200)}`);
    return;
  }
  // Initialize must finish before anything else is sent, so the session id exists.
  const p = (msg.method === 'initialize' ? forward(msg) : Promise.allSettled([...initGate]).then(() => forward(msg)))
    .catch((e) => log('ERROR', `forward crashed: ${e?.stack || e}`));
  if (msg.method === 'initialize') initGate.add(p);
  inflight.add(p);
  p.finally(() => { initGate.delete(p); inflight.delete(p); });
});
rl.on('close', async () => {
  await Promise.allSettled([...inflight]);
  log('INFO', 'stdin closed; exiting');
  // Let in-flight writes drain before exiting.
  setTimeout(() => process.exit(0), 200);
});
