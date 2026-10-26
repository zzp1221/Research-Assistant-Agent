import type {
  AssistResponse,
  DocumentInfoResponse,
  TranslationJob,
} from '../../contracts/api';
import { httpClient } from './httpClient';
import type { RequestIdentity } from './researchAssistantApi';

// Identity travels in server-signed HttpOnly cookies; no user id header is sent.
const buildIdentityHeaders = (identity?: RequestIdentity) => {
  void identity;
  return {};
};

export const getUploadDocument = async (uploadId: string, identity: RequestIdentity): Promise<DocumentInfoResponse> => {
  const response = await httpClient.get<DocumentInfoResponse>(`/api/uploads/${uploadId}/document`, {
    headers: buildIdentityHeaders(identity),
  });
  return response.data;
};

export const getUploadDocumentPage = async (
  uploadId: string,
  page: number,
  identity: RequestIdentity,
): Promise<{ upload_id: string; page: { number: number; text: string; chars: number } }> => {
  const response = await httpClient.get(`/api/uploads/${uploadId}/document/pages/${page}`, {
    headers: buildIdentityHeaders(identity),
  });
  return response.data;
};

export const assistDocument = async (
  uploadId: string,
  payload: { mode: 'translate' | 'explain'; text: string; page: number; context_pages?: number },
  identity: RequestIdentity,
): Promise<AssistResponse> => {
  const response = await httpClient.post<AssistResponse>(`/api/uploads/${uploadId}/assist`, payload, {
    headers: buildIdentityHeaders(identity),
    timeout: 120_000,
  });
  return response.data;
};

export const createTranslationJob = async (
  uploadId: string,
  payload: { start_page?: number; end_page?: number; target_lang?: string },
  identity: RequestIdentity,
): Promise<TranslationJob> => {
  const response = await httpClient.post<{ job: TranslationJob }>(
    `/api/uploads/${uploadId}/translation-jobs`,
    payload,
    { headers: buildIdentityHeaders(identity), timeout: 60_000 },
  );
  return response.data.job;
};

export const getTranslationJob = async (
  uploadId: string,
  jobId: string,
  identity: RequestIdentity,
): Promise<TranslationJob> => {
  const response = await httpClient.get<{ job: TranslationJob }>(
    `/api/uploads/${uploadId}/translation-jobs/${jobId}`,
    { headers: buildIdentityHeaders(identity) },
  );
  return response.data.job;
};

export const cancelTranslationJob = async (
  uploadId: string,
  jobId: string,
  identity: RequestIdentity,
): Promise<TranslationJob> => {
  const response = await httpClient.post<{ job: TranslationJob }>(
    `/api/uploads/${uploadId}/translation-jobs/${jobId}/cancel`,
    {},
    { headers: buildIdentityHeaders(identity) },
  );
  return response.data.job;
};

export const getTranslationPage = async (
  uploadId: string,
  jobId: string,
  page: number,
  identity: RequestIdentity,
): Promise<string> => {
  const response = await httpClient.get<{ text: string }>(
    `/api/uploads/${uploadId}/translation-jobs/${jobId}/pages/${page}`,
    { headers: buildIdentityHeaders(identity) },
  );
  return response.data.text;
};
