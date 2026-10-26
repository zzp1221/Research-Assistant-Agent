const resolveDefaultApiBaseUrl = () => {
  // 优先允许通过环境变量显式指定，便于部署或联调时覆盖。
  const envApiBaseUrl = import.meta.env.VITE_API_BASE_URL as string | undefined;
  if (envApiBaseUrl) {
    return envApiBaseUrl;
  }

  // 开发态仍固定转到后端端口，保持 Vite 联调习惯不变。
  if (typeof window !== 'undefined') {
    if (import.meta.env.DEV) {
      const { hostname } = window.location;
      const apiHost = hostname || 'localhost';
      return `http://${apiHost}:8080`;
    }

    // 生产态由单文件程序直接托管前端，此时走同源地址即可兼容自定义端口。
    return window.location.origin;
  }

  return 'http://localhost:8080';
};

export const API_BASE_URL = resolveDefaultApiBaseUrl();

// 生产构建不再注入任何静态 API token：浏览器请求经 nginx 反向代理时由
// 代理注入 Authorization。仅本地 Vite 开发态允许使用临时 token 直连后端。
export const API_AUTH_TOKEN = import.meta.env.DEV
  ? ((import.meta.env.VITE_API_AUTH_TOKEN as string | undefined)?.trim() ?? '')
  : '';
