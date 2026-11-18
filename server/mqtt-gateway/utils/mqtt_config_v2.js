require('dotenv').config();
const crypto = require('crypto');


function generatePasswordSignature(content, secretKey) {
    // Create an HMAC object using SHA256 and the secretKey
    const hmac = crypto.createHmac('sha256', secretKey);
    
    // Update the HMAC object with the clientId
    hmac.update(content);
    
    // Generate the HMAC digest in binary format
    const binarySignature = hmac.digest();
    
    // Encode the binary signature to Base64
    const base64Signature = binarySignature.toString('base64');
    
    return base64Signature;
}

function safeEqual(a, b) {
    const ha = crypto.createHash('sha256').update(String(a)).digest();
    const hb = crypto.createHash('sha256').update(String(b)).digest();
    return crypto.timingSafeEqual(ha, hb);
}

function validateMqttCredentials(clientId, username, password) {
    // Solo credenciales de entorno; si faltan, se rechaza todo.
    const envUser = process.env.MQTT_USERNAME;
    const envPass = process.env.MQTT_PASSWORD;
    if (!envUser || !envPass) {
        throw new Error('MQTT_USERNAME/MQTT_PASSWORD no configurados');
    }
    if (typeof username !== 'string' || typeof password !== 'string' ||
        !safeEqual(username, envUser) || !safeEqual(password, envPass)) {
        throw new Error('credenciales inválidas');
    }

    // 验证clientId
    if (!clientId || typeof clientId !== 'string') {
        throw new Error('clientId必须是非空字符串');
    }
    
    // 验证clientId格式（必须包含@@@分隔符）
    const clientIdParts = clientId.split('@@@');
    // 新版本 MQTT 参数
    if (clientIdParts.length !== 3) {
        throw new Error('clientId格式错误，必须包含@@@分隔符');
    }
    
    const userData = {};

    // 解析clientId中的信息
    const [groupId, macAddress, uuid] = clientIdParts;
    
    // 如果验证成功，返回解析后的有用信息
    return { 
        groupId,
        macAddress: macAddress.replace(/_/g, ':').toLowerCase(),
        uuid,
        userData
    };
}

function generateMqttConfig(groupId, macAddress, uuid, userData) {
    const endpoint = process.env.MQTT_ENDPOINT;
    const signatureKey = process.env.MQTT_SIGNATURE_KEY;
    if (!signatureKey) {
        console.warn('No signature key, skip generating MQTT config');
        return;
    }
    const deviceIdNoColon = macAddress.replace(/:/g, '_');
    const clientId = `${groupId}@@@${deviceIdNoColon}@@@${uuid}`;
    const username = Buffer.from(JSON.stringify(userData)).toString('base64');
    const password = generatePasswordSignature(clientId + '|' + username, signatureKey);
    return {
        endpoint,
        port: 8883,
        client_id: clientId,
        username,
        password,
        publish_topic: 'device-server',
        subscribe_topic: 'null' // 旧版本固件不返回此字段会出错
    }
}

module.exports = {
    generateMqttConfig,
    validateMqttCredentials
}
