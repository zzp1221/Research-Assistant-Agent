# Node 22 is required by the frontend test runner and supports Vite 8.
FROM node:22-bookworm-slim AS builder

WORKDIR /app
ARG VITE_API_AUTH_TOKEN=
ENV VITE_API_AUTH_TOKEN=${VITE_API_AUTH_TOKEN}
COPY package*.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

# 使用 Nginx 提供静态资源服务
FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
