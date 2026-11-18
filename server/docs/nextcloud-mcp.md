# nextcloud-mcp: checklist E2E y notas operativas

## Task 1: tool calling de DeepSeek vía LiteLLM

**Estado: PENDIENTE (no ejecutado).** Requiere la API key de DeepSeek, que no estaba disponible al implementar. El primer test E2E por voz lo valida. Si DeepSeek no emite tool calls, la corrección es cambiar el alias `deepseek-chat` en `server/litellm/litellm-config.yaml` por un modelo de DeepSeek que sí los emita (sin razonamiento).

## Limitación conocida

El fork de xiaozhi ejecuta solo la primera llamada a herramienta (`toolCall[0]`) por respuesta del LLM. Si el modelo emite varias tool calls en una misma respuesta, las demás se ignoran. Las tools MCP llegan al LLM con el prefijo `mcp_`. Por eso conviene pedir una acción por frase.

## Checklist E2E por voz (lo ejecuta el usuario)

- [ ] Anotar en la bitácora, corregir una entrada, leer el día y leer la semana.
- [ ] Crear una tarea duplicada (debe avisar) y luego completarla; verificar que queda en la bitácora.
- [ ] Borrar una tarea con confirmación; verificar que se rechaza la auto-confirmación.
- [ ] "¿Qué tengo hoy?"
- [ ] Guardar en una nota existente.
- [ ] Verificar que DeepSeek, vía LiteLLM, hace tool calling correcto (Task 1).

## Latencia medida

| Herramienta | Medición 1 | Medición 2 | Medición 3 | Notas |
|---|---|---|---|---|
| `resumen_del_dia` | | | | |
| `bitacora_leer` (semana) | | | | |
