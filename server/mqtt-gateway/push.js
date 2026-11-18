const http = require('http');
const crypto = require('crypto');

const MAX_BODY = 64 * 1024;

function normalizeMac(s) {
    return String(s).replace(/_/g, ':').toLowerCase();
}

function tokenMatches(header, token) {
    const m = /^Bearer (.+)$/.exec(header || '');
    if (!m) return false;
    const a = crypto.createHash('sha256').update(m[1]).digest();
    const b = crypto.createHash('sha256').update(token).digest();
    return crypto.timingSafeEqual(a, b);
}

function reply(res, status, obj) {
    const body = JSON.stringify(obj);
    res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
    res.end(body);
}

function readBody(req) {
    return new Promise((resolve, reject) => {
        const chunks = [];
        let size = 0;
        req.on('data', c => {
            size += c.length;
            if (size > MAX_BODY) { reject(new Error('too large')); req.destroy(); return; }
            chunks.push(c);
        });
        req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
        req.on('error', reject);
    });
}

function createPushServer({ token, listConnections }) {
    if (!token) {
        console.error('PUSH_TOKEN vacío: el servidor push no arranca');
        throw new Error('PUSH_TOKEN vacío');
    }
    return http.createServer(async (req, res) => {
        try {
            if (req.method !== 'POST' || req.url !== '/push') {
                return reply(res, 404, { error: 'not found' });
            }
            if (!tokenMatches(req.headers.authorization, token)) {
                return reply(res, 401, { error: 'unauthorized' });
            }
            let data;
            try {
                data = JSON.parse(await readBody(req));
            } catch (e) {
                return reply(res, 400, { error: 'invalid json' });
            }
            if (!data || typeof data !== 'object' || !data.payload || typeof data.payload !== 'object') {
                return reply(res, 400, { error: 'payload required' });
            }
            if (data.mac !== undefined && typeof data.mac !== 'string') {
                return reply(res, 400, { error: 'invalid mac' });
            }
            let targets = listConnections();
            if (data.mac) {
                const mac = normalizeMac(data.mac);
                targets = targets.filter(c => normalizeMac(c.mac) === mac);
            }
            if (targets.length === 0) {
                return reply(res, 404, { error: 'no connected device' });
            }
            const msg = JSON.stringify(data.payload);
            let sent = 0;
            for (const c of targets) {
                try { c.sendMqttMessage(msg); sent++; } catch (e) { console.error('push: fallo al enviar a un dispositivo'); }
            }
            return reply(res, 200, { sent });
        } catch (e) {
            console.error('push: error interno');
            return reply(res, 500, { error: 'internal' });
        }
    });
}

module.exports = { createPushServer, normalizeMac };
