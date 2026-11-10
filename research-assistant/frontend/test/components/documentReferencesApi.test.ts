import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { DocumentReferenceDraft } from '../../src/contracts/api';

vi.mock('../../src/services/api/httpClient', () => ({ httpClient: { post: vi.fn() } }));

import { httpClient } from '../../src/services/api/httpClient';
import { chat, serializeDocumentReferences } from '../../src/services/api/researchAssistantApi';

const reference: DocumentReferenceDraft = {
  id: 'draft-1', sessionId: 'session-1', upload_id: 'upload-1',
  document_name: 'attention.pdf', page: 3, quote: 'The selected passage',
};

describe('document references on the chat API', () => {
  beforeEach(() => vi.clearAllMocks());

  it('serializes the frozen source identity and page without frontend draft fields', async () => {
    vi.mocked(httpClient.post).mockResolvedValue({ data: { response: 'answer' } });
    await chat('Explain this', { userId: 'user-1', sessionId: 'cookie-session' }, {
      sessionId: 'session-1', documentReferences: [reference],
    });
    expect(httpClient.post).toHaveBeenCalledWith('/api/chat', {
      message: 'Explain this', session_id: 'session-1',
      document_references: [{ upload_id: 'upload-1', document_name: 'attention.pdf', page: 3, quote: 'The selected passage' }],
    }, { headers: {} });
    expect(reference).toMatchObject({ id: 'draft-1', sessionId: 'session-1', page: 3 });
  });

  it.each([0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY])('rejects page %s without requesting a different page', async (page) => {
    await expect(chat('Question', { userId: '', sessionId: 'session-1' }, {
      documentReferences: [{ ...reference, page }],
    })).rejects.toThrow('页码无效');
    expect(httpClient.post).not.toHaveBeenCalled();
  });

  it('counts Unicode code points for the quote boundary and enforces the reference count', () => {
    expect(serializeDocumentReferences([{ ...reference, quote: '📚'.repeat(2000) }])[0]?.quote).toHaveLength(4000);
    expect(() => serializeDocumentReferences([{ ...reference, quote: '📚'.repeat(2001) }])).toThrow('2000');
    expect(() => serializeDocumentReferences(Array.from({ length: 5 }, () => reference))).toThrow('4');
    expect(() => serializeDocumentReferences([{ ...reference, upload_id: '' }])).toThrow('缺少文档');
  });

  it('omits references for an ordinary chat message', async () => {
    vi.mocked(httpClient.post).mockResolvedValue({ data: { response: 'answer' } });
    await chat('Hello', { userId: '', sessionId: 'session-1' });
    expect(httpClient.post).toHaveBeenCalledWith('/api/chat', { message: 'Hello' }, { headers: {} });
  });
});
