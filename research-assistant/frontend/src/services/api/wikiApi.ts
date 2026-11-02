import type {
  WikiBuildJob,
  WikiLink,
  WikiLinkList,
  WikiPaperList,
  WikiPaperPage,
  WikiPoint,
  WikiPointList,
  WikiStats,
} from '../../contracts/api';
import { httpClient } from './httpClient';

export interface WikiPaperPayload {
  title?: string;
  authors?: string[];
  year?: number;
  venue?: string;
  arxiv_id?: string;
  doi?: string;
  abstract?: string;
  tldr?: string;
  project_id?: string;
  document_id?: string;
}

export const listWikiPapers = async (params: { q?: string; project?: string; status?: string; limit?: number; cursor?: number } = {}): Promise<WikiPaperList> => {
  const response = await httpClient.get<WikiPaperList>('/api/wiki/papers', { params });
  return response.data;
};

export const getWikiPaper = async (paperID: string): Promise<WikiPaperPage> => {
  const response = await httpClient.get<{ paper: WikiPaperPage }>(`/api/wiki/papers/${paperID}`);
  return response.data.paper;
};

export const createWikiPaper = async (payload: WikiPaperPayload): Promise<WikiPaperPage> => {
  const response = await httpClient.post<{ paper: WikiPaperPage }>('/api/wiki/papers', payload);
  return response.data.paper;
};

export const updateWikiPaper = async (paperID: string, payload: Partial<WikiPaperPayload> & { archived?: boolean }): Promise<WikiPaperPage> => {
  const response = await httpClient.patch<{ paper: WikiPaperPage }>(`/api/wiki/papers/${paperID}`, payload);
  return response.data.paper;
};

export const archiveWikiPaper = async (paperID: string): Promise<WikiPaperPage> => {
  const response = await httpClient.delete<{ paper: WikiPaperPage }>(`/api/wiki/papers/${paperID}`);
  return response.data.paper;
};

export const listWikiPoints = async (paperID: string, params: { kind?: string; limit?: number; cursor?: number } = {}): Promise<WikiPointList> => {
  const response = await httpClient.get<WikiPointList>(`/api/wiki/papers/${paperID}/points`, { params });
  return response.data;
};

export const createWikiPoint = async (paperID: string, text: string, title?: string): Promise<WikiPoint> => {
  const response = await httpClient.post<{ point: WikiPoint }>(`/api/wiki/papers/${paperID}/points`, { text, title });
  return response.data.point;
};

export const updateWikiPoint = async (pointID: string, payload: { title?: string; text?: string; status?: string; importance?: number; pinned?: boolean }): Promise<void> => {
  await httpClient.patch(`/api/wiki/points/${pointID}`, payload);
};

export const deleteWikiPoint = async (pointID: string): Promise<void> => {
  await httpClient.delete(`/api/wiki/points/${pointID}`);
};

export const listWikiPaperLinks = async (paperID: string, params: { type?: string; status?: string; limit?: number; cursor?: number } = {}): Promise<WikiLinkList> => {
  const response = await httpClient.get<WikiLinkList>(`/api/wiki/papers/${paperID}/links`, { params });
  return response.data;
};

export const listWikiLinks = async (params: { status?: string; type?: string; limit?: number; cursor?: number } = {}): Promise<WikiLinkList> => {
  const response = await httpClient.get<WikiLinkList>('/api/wiki/links', { params });
  return response.data;
};

export const confirmWikiLink = async (linkID: string): Promise<WikiLink> => {
  const response = await httpClient.post<{ link: WikiLink }>(`/api/wiki/links/${linkID}/confirm`);
  return response.data.link;
};

export const rejectWikiLink = async (linkID: string, reason: string): Promise<WikiLink> => {
  const response = await httpClient.post<{ link: WikiLink }>(`/api/wiki/links/${linkID}/reject`, { reason });
  return response.data.link;
};

export const startWikiBuild = async (mode: 'incremental' | 'full' = 'incremental'): Promise<WikiBuildJob> => {
  const response = await httpClient.post<WikiBuildJob>('/api/wiki/build', { mode });
  return response.data;
};

export const getWikiBuild = async (jobID: string): Promise<WikiBuildJob> => {
  const response = await httpClient.get<WikiBuildJob>(`/api/wiki/build/${jobID}`);
  return response.data;
};

export const getWikiStats = async (): Promise<WikiStats> => {
  const response = await httpClient.get<WikiStats>('/api/wiki/stats');
  return response.data;
};
