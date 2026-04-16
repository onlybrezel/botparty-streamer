import http from 'node:http';
import { AccessToken } from 'livekit-server-sdk';

const port = Number(process.env.PORT || 8088);
const apiKey = process.env.LIVEKIT_API_KEY;
const apiSecret = process.env.LIVEKIT_API_SECRET;
const issuerBearer = process.env.TOKEN_ISSUER_BEARER || '';
const defaultRoom = process.env.DEFAULT_ROOM || '';
const defaultTtl = Number(process.env.DEFAULT_TOKEN_TTL_SECONDS || 300);

if (!apiKey || !apiSecret) {
  throw new Error('LIVEKIT_API_KEY and LIVEKIT_API_SECRET are required');
}

const deviceSecrets = new Map();
for (const item of (process.env.DEVICE_SECRETS || '').split(';')) {
  const trimmed = item.trim();
  if (!trimmed) {
    continue;
  }
  const eq = trimmed.indexOf('=');
  if (eq <= 0) {
    continue;
  }
  const id = trimmed.slice(0, eq).trim();
  const secret = trimmed.slice(eq + 1).trim();
  if (id && secret) {
    deviceSecrets.set(id, secret);
  }
}

if (!issuerBearer && deviceSecrets.size === 0) {
  throw new Error(
    'refusing to start token backend without auth: set TOKEN_ISSUER_BEARER and/or DEVICE_SECRETS',
  );
}

function sendJson(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
  });
  res.end(body);
}

function parseJsonBody(req) {
  return new Promise((resolve, reject) => {
    let raw = '';
    req.setEncoding('utf8');
    req.on('data', (chunk) => {
      raw += chunk;
      if (raw.length > 16 * 1024) {
        reject(new Error('payload too large'));
      }
    });
    req.on('end', () => {
      if (!raw.trim()) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch (err) {
        reject(err);
      }
    });
    req.on('error', reject);
  });
}

function getHeader(req, name) {
  const value = req.headers[name.toLowerCase()];
  return typeof value === 'string' ? value : '';
}

function isAuthorized(req) {
  if (issuerBearer) {
    const auth = getHeader(req, 'authorization');
    if (auth !== `Bearer ${issuerBearer}`) {
      return false;
    }
  }

  if (deviceSecrets.size === 0) {
    return true;
  }

  const deviceId = getHeader(req, 'x-device-id');
  const deviceSecret = getHeader(req, 'x-device-secret');
  const expected = deviceSecrets.get(deviceId);
  return Boolean(expected && expected === deviceSecret);
}

const server = http.createServer(async (req, res) => {
  if (req.method === 'GET' && req.url === '/healthz') {
    sendJson(res, 200, { ok: true });
    return;
  }

  if (req.method === 'POST' && req.url === '/token') {
    if (!isAuthorized(req)) {
      sendJson(res, 401, { error: 'unauthorized' });
      return;
    }

    let body = {};
    try {
      body = await parseJsonBody(req);
    } catch (err) {
      sendJson(res, 400, { error: 'invalid JSON body' });
      return;
    }

    const room = String(body.room || defaultRoom || '').trim();
    const identity = String(body.identity || '').trim();
    const name = String(body.name || identity).trim();
    const ttlSeconds = Math.max(30, Math.min(900, Number(body.ttlSeconds || defaultTtl)));

    if (!room || !identity) {
      sendJson(res, 400, { error: 'room and identity required' });
      return;
    }

    const at = new AccessToken(apiKey, apiSecret, {
      identity,
      name,
      ttl: `${ttlSeconds}s`,
    });

    at.addGrant({
      room,
      roomJoin: true,
      canPublish: true,
      canSubscribe: false,
      canPublishData: false,
    });

    const token = await at.toJwt();
    sendJson(res, 200, { token, ttlSeconds });
    return;
  }

  sendJson(res, 404, { error: 'not found' });
});

server.listen(port, () => {
  console.log(`token backend listening on :${port}`);
});
