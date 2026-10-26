import axios from 'axios';
import { API_AUTH_TOKEN, API_BASE_URL } from '../../config/env';

export const httpClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 60_000,
  withCredentials: true,
});

// Only the Vite dev server injects a token (see config/env.ts). Production
// builds never contain a static token; nginx adds Authorization server-side.
if (API_AUTH_TOKEN) {
  httpClient.defaults.headers.common.Authorization = `Bearer ${API_AUTH_TOKEN}`;
}
