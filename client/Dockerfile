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

FROM caddy:2.10-alpine
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
EXPOSE 80
