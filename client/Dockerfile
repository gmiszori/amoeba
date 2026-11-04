FROM node:26-alpine@sha256:c6ad9e9c1d80f6eb607c26b4b0a132ff7a2344fad1c78ea3baa2a7d4c8de6d94 AS source
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .

# Used by docker-compose.dev.yml: esbuild watches, rebuilds and serves on
# :4321; compose watch syncs source edits into the running container.
FROM source AS dev
EXPOSE 4321
CMD ["npm", "run", "dev"]

FROM source AS build
RUN npm run build

FROM caddy:2.11-alpine@sha256:70b3180738b46cd88e49b300d790ae448a0aca3f58edef0f000bb57d619a9ab3
# The stock binary carries a file capability for binding ports 80/443, but a
# binary with file capabilities can't start in a container with all
# capabilities dropped. We drop them and let compose allow unprivileged
# binding of 80/443 instead, then run as nobody. /data holds the TLS
# certificates and must be writable by that user.
RUN setcap -r /usr/bin/caddy && chown 65534:65534 /data /config
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
USER 65534:65534
EXPOSE 80 443
