FROM node:22-alpine AS source
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .

# Used by docker-compose.dev.yml for the hot-reloading dev server; compose
# watch syncs source edits into the running container.
FROM source AS dev
EXPOSE 4321
CMD ["npm", "run", "dev"]

FROM source AS build
RUN npm run build

FROM caddy:2.11-alpine
# The stock binary carries a file capability for binding port 80. We listen on
# 8080 instead and drop all capabilities, which a binary with file
# capabilities can't be started under, so remove it and run as nobody.
RUN setcap -r /usr/bin/caddy
ENV XDG_CONFIG_HOME=/tmp/caddy/config XDG_DATA_HOME=/tmp/caddy/data
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
USER 65534:65534
EXPOSE 8080
