# Node 22 is required by the frontend test runner and supports Vite 8.
FROM node:22-bookworm-slim AS builder

WORKDIR /app
# No API token is compiled into the bundle. Production auth is injected by the
# nginx reverse proxy at request time.
COPY package*.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

# 使用 Nginx 提供静态资源服务
FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
# The official nginx entrypoint renders /etc/nginx/templates/*.template with
# envsubst, so API_AUTH_TOKEN is injected at container start, never at build.
COPY nginx.conf.template /etc/nginx/templates/default.conf.template

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
