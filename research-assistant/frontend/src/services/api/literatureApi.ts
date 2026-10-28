import type {
  CitationGraph,
  LiteratureAttachResponse,
  LiteratureImportResponse,
  LiteratureRecord,
  LiteratureRecordListResponse,
  LiteratureSearchResponse,
  WebFetchResult,
} from '../../contracts/api';
import { httpClient } from './httpClient';

export interface LiteratureSearchParams {
  query: string;
  sources?: string[];
  yearFrom?: number;
  limit?: number;
  sort?: 'relevance' | 'citations' | 'year';
}

export const searchLiterature = async (params: LiteratureSearchParams): Promise<LiteratureSearchResponse> => {
  const response = await httpClient.get<LiteratureSearchResponse>('/api/literature/search', {
    params: {
      q: params.query,
      sources: params.sources?.length ? params.sources.join(',') : undefined,
      year_from: params.yearFrom || undefined,
      limit: params.limit ?? 20,
      sort: params.sort ?? 'relevance',
    },
    timeout: 60_000,
  });
  return response.data;
};

export const importLiterature = async (payload: {
  ref: string;
  attach?: boolean;
  metadataOnly?: boolean;
}): Promise<LiteratureImportResponse> => {
  const response = await httpClient.post<LiteratureImportResponse>(
    '/api/literature/import',
    { ref: payload.ref, attach: payload.attach ?? true, metadata_only: payload.metadataOnly ?? false },
    { timeout: 180_000 },
  );
  return response.data;
};

export const listLiteratureRecords = async (params: {
  query?: string;
  tag?: string;
  limit?: number;
  cursor?: number;
}): Promise<LiteratureRecordListResponse> => {
  const response = await httpClient.get<LiteratureRecordListResponse>('/api/literature/records', {
    params: {
      q: params.query || undefined,
      tag: params.tag || undefined,
      limit: params.limit ?? 50,
      cursor: params.cursor || undefined,
    },
  });
  return response.data;
};

export const getLiteratureRecord = async (id: string): Promise<LiteratureRecord> => {
  const response = await httpClient.get<{ record: LiteratureRecord }>(`/api/literature/records/${id}`);
  return response.data.record;
};

export const deleteLiteratureRecord = async (id: string): Promise<void> => {
  await httpClient.delete(`/api/literature/records/${id}`);
};

export const attachLiteratureRecord = async (id: string, sessionId?: string): Promise<LiteratureAttachResponse> => {
  const response = await httpClient.post<LiteratureAttachResponse>(`/api/literature/records/${id}/attach`, {
    session_id: sessionId,
  });
  return response.data;
};

export const detachLiteratureRecord = async (id: string, sessionId?: string): Promise<LiteratureRecord> => {
  const response = await httpClient.delete<LiteratureAttachResponse>(`/api/literature/records/${id}/attach`, {
    data: { session_id: sessionId },
  });
  return response.data.record;
};

export const literatureBibTeXUrl = (id: string): string => `/api/literature/records/${id}/bibtex`;

export const getLiteratureCitations = async (doi: string, limit = 25): Promise<CitationGraph> => {
  const response = await httpClient.get<CitationGraph>(`/api/literature/citations/${encodeURIComponent(doi)}`, {
    params: { limit },
    timeout: 60_000,
  });
  return response.data;
};

export const fetchWebPage = async (url: string): Promise<WebFetchResult> => {
  const response = await httpClient.post<WebFetchResult>('/api/web/fetch', { url }, { timeout: 60_000 });
  return response.data;
};
