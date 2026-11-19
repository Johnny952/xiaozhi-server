const test = require('node:test');
const assert = require('node:assert');
const { encodeAudio, decodeAudio, PROTOCOL_VERSION } = require('../framing');

// El fork Go (xiaozhi-server-go) no implementa el protocolo binario v2:
// trata cada frame binario como opus crudo en ambos sentidos.
test('el bridge anuncia protocolo 1', () => {
    assert.strictEqual(PROTOCOL_VERSION, 1);
});

test('encodeAudio manda el opus tal cual, sin cabecera', () => {
    const opus = Buffer.from([0x58, 0x01, 0x02, 0x03]);
    assert.deepStrictEqual(encodeAudio(opus, 1234), opus);
});

test('decodeAudio devuelve el frame completo como opus', () => {
    const opus = Buffer.from([0x58, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x10]);
    const { opus: out, timestamp } = decodeAudio(opus, 40);
    assert.deepStrictEqual(out, opus);
    assert.strictEqual(timestamp, 40);
});

test('decodeAudio con frames de menos de 16 bytes no lanza', () => {
    const opus = Buffer.from([0xf8, 0xff, 0xfe]);
    assert.deepStrictEqual(decodeAudio(opus, 0).opus, opus);
});
