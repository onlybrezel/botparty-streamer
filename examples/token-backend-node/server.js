import 'dotenv/config';
import express from 'express';
import { AccessToken } from 'livekit-server-sdk';

const app = express();
app.use(express.json({ limit: '16kb' }));

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

function isAuthorized(req) {
  if (issuerBearer) {
    const auth = req.get('authorization') || '';
    if (auth !== `Bearer ${issuerBearer}`) {
      return false;
    }
  }

  if (deviceSecrets.size === 0) {
    return true;
  }

  const deviceId = req.get('x-device-id') || '';
  const deviceSecret = req.get('x-device-secret') || '';
  const expected = deviceSecrets.get(deviceId);
  return Boolean(expected && expected === deviceSecret);
}

app.post('/token', async (req, res) => {
  if (!isAuthorized(req)) {
    return res.status(401).json({ error: 'unauthorized' });
  }

  const room = String(req.body.room || defaultRoom || '').trim();
  const identity = String(req.body.identity || '').trim();
  const name = String(req.body.name || identity).trim();
  const ttlSeconds = Math.max(30, Math.min(900, Number(req.body.ttlSeconds || defaultTtl)));

  if (!room || !identity) {
    return res.status(400).json({ error: 'room and identity required' });
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
  return res.json({ token, ttlSeconds });
});

app.get('/healthz', (_req, res) => {
  res.json({ ok: true });
});

app.listen(port, () => {
  console.log(`token backend listening on :${port}`);
});
