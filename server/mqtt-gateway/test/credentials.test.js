const test = require('node:test');
const assert = require('node:assert');
const { validateMqttCredentials } = require('../utils/mqtt_config_v2');

const CID = 'GID_test@@@AA_BB_CC_DD_EE_FF@@@uuid-1';

test('rechaza todo si faltan MQTT_USERNAME/MQTT_PASSWORD', () => {
    delete process.env.MQTT_USERNAME;
    delete process.env.MQTT_PASSWORD;
    assert.throws(() => validateMqttCredentials(CID, 'u', 'p'));
});

test('acepta credenciales de entorno y normaliza la MAC', () => {
    process.env.MQTT_USERNAME = 'u';
    process.env.MQTT_PASSWORD = 'p';
    const v = validateMqttCredentials(CID, 'u', 'p');
    assert.strictEqual(v.macAddress, 'aa:bb:cc:dd:ee:ff');
    assert.strictEqual(v.uuid, 'uuid-1');
});

test('rechaza credenciales incorrectas', () => {
    process.env.MQTT_USERNAME = 'u';
    process.env.MQTT_PASSWORD = 'p';
    assert.throws(() => validateMqttCredentials(CID, 'u', 'x'));
    assert.throws(() => validateMqttCredentials(CID, 'x', 'p'));
});
