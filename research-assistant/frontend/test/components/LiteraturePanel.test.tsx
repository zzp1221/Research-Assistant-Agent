import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureRecord, LiteratureResult, UploadedFile } from '../../src/contracts/api';

const results: LiteratureResult[] = [
  {
    source: 'crossref', id: '10.1234/a', title: 'Graph Neural Networks: A Survey',
    authors: ['Ada Lovelace'], year: 2021, venue: 'NeurIPS', citations: 42,
    doi: '10.1234/a', open_access: true, abstract: 'A survey of graph neural networks.',
  },
  {
    source: 'openalex', id: 'W2', title: 'Message Passing Revisited',
    authors: ['Alan Turing'], year: 2020, citations: 7, doi: '10.1234/b',
  },
];

const records: LiteratureRecord[] = [
  {
    id: 'rec-1', source: 'crossref', title: 'Saved Paper', authors: ['Ada'], year: 2022,
    tags: ['gnn'], upload_id: 'upload-1', created_at: '2026-10-27T00:00:00Z', updated_at: '2026-10-27T00:00:00Z',
  },
];

const upload: UploadedFile = {
  id: 'upload-1', name: 'Saved Paper.pdf', content_type: 'application/pdf', size: 2048,
  sha256: 'sha', content_url: '/api/uploads/upload-1/content', created_at: '2026-10-27T00:00:00Z',
};

const searchLiterature = vi.fn();
const importLiterature = vi.fn();
const listLiteratureRecords = vi.fn();
const deleteLiteratureRecord = vi.fn();
const attachLiteratureRecord = vi.fn();
const detachLiteratureRecord = vi.fn();
const getLiteratureCitations = vi.fn();
const fetchWebPage = vi.fn();
const getUpload = vi.fn();

vi.mock('../../src/services/api/literatureApi', () => ({
  searchLiterature: (params: unknown) => searchLiterature(params),
  importLiterature: (payload: unknown) => importLiterature(payload),
  listLiteratureRecords: (params: unknown) => listLiteratureRecords(params),
  deleteLiteratureRecord: (id: string) => deleteLiteratureRecord(id),
  attachLiteratureRecord: (id: string, sessionId?: string) => attachLiteratureRecord(id, sessionId),
  detachLiteratureRecord: (id: string, sessionId?: string) => detachLiteratureRecord(id, sessionId),
  getLiteratureCitations: (doi: string, limit?: number) => getLiteratureCitations(doi, limit),
  literatureBibTeXUrl: (id: string) => '/api/literature/records/' + id + '/bibtex',
  fetchWebPage: (url: string) => fetchWebPage(url),
}));

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  getUpload: (id: string) => getUpload(id),
}));

const { LiteraturePanel } = await import('../../src/features/literature/LiteraturePanel');

beforeEach(() => {
  vi.clearAllMocks();
  searchLiterature.mockResolvedValue({ query: 'gnn', results, count: 2, warnings: [], sort: 'relevance' });
  importLiterature.mockResolvedValue({ metadata: results[0], source: 'crossref', attached: false, metadata_only: true, record: records[0] });
  listLiteratureRecords.mockResolvedValue({ records, count: 1, next_cursor: 0 });
  deleteLiteratureRecord.mockResolvedValue(undefined);
  attachLiteratureRecord.mockResolvedValue({ record: records[0], upload });
  detachLiteratureRecord.mockResolvedValue(records[0]);
  getUpload.mockResolvedValue(upload);
  getLiteratureCitations.mockResolvedValue({ doi: '10.1234/a', cited_by_count: 1, references: [], cited_by: [] });
  fetchWebPage.mockResolvedValue({ title: 'Example', url: 'https://example.com', text: 'Body', excerpt: 'Body', links: [], fetched_at: '', content_type: 'text/html', word_count: 1, cached: false });
});

describe('LiteraturePanel', () => {
  it('searches providers and renders result cards with badges', async () => {
    const user = userEvent.setup();
    render(<LiteraturePanel />);
    await user.type(screen.getByLabelText('文献检索关键词'), 'gnn');
    await user.click(screen.getByRole('button', { name: '执行检索' }));
    expect(await screen.findByText('Graph Neural Networks: A Survey')).toBeTruthy();
    expect(screen.getByText('Message Passing Revisited')).toBeTruthy();
    expect(screen.getByText('OA')).toBeTruthy();
    expect(searchLiterature).toHaveBeenCalledWith(expect.objectContaining({ query: 'gnn', sort: 'relevance' }));
  });

  it('imports metadata-only without downloading a PDF', async () => {
    const user = userEvent.setup();
    render(<LiteraturePanel />);
    await user.type(screen.getByLabelText('文献检索关键词'), 'gnn');
    await user.click(screen.getByRole('button', { name: '执行检索' }));
    await screen.findByText('Graph Neural Networks: A Survey');
    await user.click(screen.getAllByRole('button', { name: '仅保存元数据' })[0]);
    await waitFor(() => expect(importLiterature).toHaveBeenCalledWith(expect.objectContaining({ ref: '10.1234/a', metadataOnly: true })));
    expect(await screen.findByText(/已保存到文献库/)).toBeTruthy();
  });

  it('lists the owner library and supports deletion', async () => {
    const user = userEvent.setup();
    render(<LiteraturePanel />);
    await user.click(screen.getByRole('tab', { name: '我的文献库' }));
    expect(await screen.findByText('Saved Paper')).toBeTruthy();
    expect(screen.getByText('gnn')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: /删除/ }));
    await waitFor(() => expect(deleteLiteratureRecord).toHaveBeenCalledWith('rec-1'));
  });

  it('attaches a saved record to the active session and reports the attachment', async () => {
    const onAttachToSession = vi.fn().mockReturnValue('added');
    const user = userEvent.setup();
    render(<LiteraturePanel activeSessionId="session-1" onAttachToSession={onAttachToSession} />);
    await user.click(screen.getByRole('tab', { name: '我的文献库' }));
    await screen.findByText('Saved Paper');
    await user.click(screen.getByRole('button', { name: '加入会话' }));
    await waitFor(() => expect(getUpload).toHaveBeenCalledWith('upload-1'));
    expect(attachLiteratureRecord).toHaveBeenCalledWith('rec-1', 'session-1');
    expect(onAttachToSession).toHaveBeenCalledWith(upload, records[0]);
    expect(await screen.findByText(/已加入当前会话/)).toBeTruthy();
  });

  it('shows why a metadata-only record cannot be attached yet', async () => {
    listLiteratureRecords.mockResolvedValue({
      records: [{ ...records[0], upload_id: undefined }], count: 1, next_cursor: 0,
    });
    const user = userEvent.setup();
    render(<LiteraturePanel activeSessionId="session-1" onAttachToSession={vi.fn()} />);
    await user.click(screen.getByRole('tab', { name: '我的文献库' }));
    await screen.findByText('Saved Paper');
    const attach = screen.getByRole('button', { name: '加入会话' });
    expect(attach).toBeDisabled();
    expect(attach).toHaveAttribute('title', '该条目只有元数据，请先导入 PDF');
  });
});
