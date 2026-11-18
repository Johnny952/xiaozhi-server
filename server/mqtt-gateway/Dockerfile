FROM node:20-alpine
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY . .
ENV MQTT_PORT=1883 UDP_PORT=8884 PUSH_PORT=8007
# PUBLIC_IP, PUSH_TOKEN, MQTT_USERNAME y MQTT_PASSWORD se pasan por entorno.
# El puerto push (8007) es interno: no se publica al host.
EXPOSE 1883 8884/udp
USER node
CMD ["node", "app.js"]
