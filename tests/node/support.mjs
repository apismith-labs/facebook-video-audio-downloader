/** Shared test helpers: a local Range-capable media server and a fake Apify transport. */
import http from 'node:http';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';

export async function withServer(payload, { supportRange = true } = {}, run) {
  const server = http.createServer((request, response) => {
    const header = supportRange ? request.headers.range : undefined;
    let start = 0;
    if (header?.startsWith('bytes=')) {
      start = Number(header.slice(6).split('-')[0] || 0);
      if (start >= payload.length) {
        response.writeHead(416, { 'Content-Range': `bytes */${payload.length}`, 'Content-Length': '0' });
        response.end();
        return;
      }
    }
    const body = payload.subarray(start);
    const headers = { 'Content-Type': 'video/mp4', 'Content-Length': String(body.length) };
    if (start > 0) headers['Content-Range'] = `bytes ${start}-${payload.length - 1}/${payload.length}`;
    response.writeHead(start > 0 ? 206 : 200, headers);
    response.end(body);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}/media.mp4`;
  try {
    return await run(url);
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

export async function tempDir() {
  return fs.mkdtemp(path.join(os.tmpdir(), 'fbdl-'));
}

export class FakeTransport {
  constructor(responses = []) {
    this.responses = [...responses];
    this.requests = [];
  }

  async request(method, requestPath, body, options = {}) {
    this.requests.push({ method: method.toUpperCase(), path: requestPath, body, options });
    if (this.responses.length === 0) throw new Error('No synthetic transport response queued');
    const next = this.responses.shift();
    if (next instanceof Error) throw next;
    return next;
  }

  get postCount() {
    return this.requests.filter((entry) => entry.method === 'POST').length;
  }
}
