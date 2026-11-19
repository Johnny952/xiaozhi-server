// Framing de audio entre el gateway y el servidor de chat (xiaozhi-server-go).
// El fork no implementa el protocolo binario v2 (cabecera de 16 bytes):
// lee y escribe cada frame binario del WebSocket como opus crudo.
const PROTOCOL_VERSION = 1;

function encodeAudio(opus) {
    return opus;
}

// El frame no trae timestamp; quien llama lo genera localmente.
function decodeAudio(data, timestamp) {
    return { opus: data, timestamp };
}

module.exports = { PROTOCOL_VERSION, encodeAudio, decodeAudio };
