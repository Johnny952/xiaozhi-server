const test = require('node:test');
const assert = require('node:assert');
const { createPushServer, normalizeMac } = require('../push');

const TOKEN = 'secret-token';

function fakeConn(mac) {
    return { mac, sent: [], sendMqttMessage(s) { this.sent.push(s); } };
}

async function withServer(conns, fn) {
    const server = createPushServer({ token: TOKEN, listConnections: () => conns });
    await new Promise(r => server.listen(0, '127.0.0.1', r));
    const base = `http://127.0.0.1:${server.address().port}`;
    try { await fn(base); } finally { await new Promise(r => server.close(r)); }
}

function post(base, body, token = TOKEN) {
    const headers = { 'Content-Type': 'application/json' };
    if (token !== null) headers.Authorization = `Bearer ${token}`;
    return fetch(`${base}/push`, { method: 'POST', headers, body: typeof body === 'string' ? body : JSON.stringify(body) });
}

test('rechaza sin token', async () => {
    await withServer([fakeConn('aa:bb:cc:dd:ee:ff')], async base => {
        const res = await post(base, { payload: { a: 1 } }, null);
        assert.strictEqual(res.status, 401);
    });
});

test('rechaza token incorrecto', async () => {
    await withServer([fakeConn('aa:bb:cc:dd:ee:ff')], async base => {
        const res = await post(base, { payload: { a: 1 } }, 'otro');
        assert.strictEqual(res.status, 401);
    });
});

test('400 sin payload o con JSON inválido', async () => {
    await withServer([fakeConn('aa:bb:cc:dd:ee:ff')], async base => {
        assert.strictEqual((await post(base, { mac: 'aa:bb:cc:dd:ee:ff' })).status, 400);
        assert.strictEqual((await post(base, '{no json')).status, 400);
    });
});

test('push normaliza MAC con guiones bajos y mayúsculas', async () => {
    const c = fakeConn('aa:bb:cc:dd:ee:ff');
    await withServer([c], async base => {
        const payload = { type: 'alert', message: 'hola' };
        const res = await post(base, { mac: 'AA_BB_CC_DD_EE_FF', payload });
        assert.strictEqual(res.status, 200);
        assert.deepStrictEqual(await res.json(), { sent: 1 });
        assert.deepStrictEqual(c.sent, [JSON.stringify(payload)]);
    });
});

test('404 si la mac no está conectada', async () => {
    await withServer([fakeConn('aa:bb:cc:dd:ee:ff')], async base => {
        const res = await post(base, { mac: '11:22:33:44:55:66', payload: { a: 1 } });
        assert.strictEqual(res.status, 404);
    });
});

test('sin mac envía a todas', async () => {
    const a = fakeConn('aa:bb:cc:dd:ee:ff');
    const b = fakeConn('11:22:33:44:55:66');
    await withServer([a, b], async base => {
        const res = await post(base, { payload: { a: 1 } });
        assert.strictEqual(res.status, 200);
        assert.deepStrictEqual(await res.json(), { sent: 2 });
        assert.strictEqual(a.sent.length, 1);
        assert.strictEqual(b.sent.length, 1);
    });
});

test('sin mac y nadie conectado', async () => {
    await withServer([], async base => {
        const res = await post(base, { payload: { a: 1 } });
        assert.strictEqual(res.status, 404);
    });
});

test('normalizeMac', () => {
    assert.strictEqual(normalizeMac('AA_BB_CC_DD_EE_FF'), 'aa:bb:cc:dd:ee:ff');
});
