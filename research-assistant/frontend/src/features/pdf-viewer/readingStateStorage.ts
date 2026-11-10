// Reading position persistence for the PDF reader.
//
// Only stable, validated scalars are stored: the owner id, the chat session id,
// the upload identity, the document name, the one-based page and the zoom
// scale. A temporary blob URL is never persisted — after a refresh the document
// is re-fetched through `GET /api/uploads/:id` for the current identity.
//
// The active full-text translation job is persisted the same way (owner,
// session, upload, job id, last known status) so a refresh or a reopened reader
// resumes the running server job instead of silently starting a second one.

export interface PdfReadingState {
  /** One-based page number. */
  page: number;
  /** Zoom scale as reported by the viewer (1 = 100%). */
  scale: number;
}

export interface PersistedReadingEntry extends PdfReadingState {
  uploadId: string;
  documentName: string;
  updatedAt: string;
}

export interface PersistedTranslationRecord {
  uploadId: string;
  jobId: string;
  status: string;
  updatedAt: string;
}

export const PDF_READING_STORAGE_KEY = 'research-assistant.pdf-reading.v1';
export const PDF_TRANSLATION_STORAGE_KEY = 'research-assistant.pdf-translation.v1';
export const MIN_READING_SCALE = 0.25;
export const MAX_READING_SCALE = 4;
export const DEFAULT_READING_STATE: PdfReadingState = { page: 1, scale: 1 };

const ANONYMOUS_OWNER = 'anonymous';
const UNSCOPED_SESSION = 'session';

export const clampReadingScale = (scale: number): number => {
  if (!Number.isFinite(scale)) return DEFAULT_READING_STATE.scale;
  return Math.min(MAX_READING_SCALE, Math.max(MIN_READING_SCALE, scale));
};

export const normalizeReadingPage = (page: number): number => {
  if (!Number.isFinite(page)) return DEFAULT_READING_STATE.page;
  return Math.max(1, Math.floor(page));
};

// normalizeReadingState accepts any persisted/partial shape and returns null
// when nothing usable is present, so a corrupt cache falls back to defaults
// instead of moving the reader to a nonsense page.
export const normalizeReadingState = (raw: unknown): PdfReadingState | null => {
  if (!raw || typeof raw !== 'object') return null;
  const candidate = raw as Partial<PdfReadingState>;
  const hasPage = typeof candidate.page === 'number' && Number.isFinite(candidate.page);
  const hasScale = typeof candidate.scale === 'number' && Number.isFinite(candidate.scale);
  if (!hasPage && !hasScale) return null;
  return {
    page: hasPage ? normalizeReadingPage(candidate.page as number) : DEFAULT_READING_STATE.page,
    scale: hasScale ? clampReadingScale(candidate.scale as number) : DEFAULT_READING_STATE.scale,
  };
};

export const readingStoreKey = (ownerId: string, sessionId: string, uploadId: string): string =>
  `${ownerId || ANONYMOUS_OWNER}|${sessionId || UNSCOPED_SESSION}|${uploadId}`;

type ReadingStore = Record<string, PersistedReadingEntry>;

const readStore = (): ReadingStore => {
  if (typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(PDF_READING_STORAGE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return {};
    const entries: ReadingStore = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (!value || typeof value !== 'object') continue;
      const candidate = value as Partial<PersistedReadingEntry>;
      const state = normalizeReadingState(candidate);
      if (!state) continue;
      if (typeof candidate.uploadId !== 'string' || !candidate.uploadId.trim()) continue;
      entries[key] = {
        uploadId: candidate.uploadId.trim(),
        documentName: typeof candidate.documentName === 'string' ? candidate.documentName : '',
        page: state.page,
        scale: state.scale,
        updatedAt: typeof candidate.updatedAt === 'string' ? candidate.updatedAt : '',
      };
    }
    return entries;
  } catch {
    return {};
  }
};

const writeStore = (store: ReadingStore) => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(PDF_READING_STORAGE_KEY, JSON.stringify(store));
  } catch {
    // Reading position is a convenience; a blocked/quota-full storage must not
    // break the reader.
  }
};

export const readReadingEntry = (ownerId: string, sessionId: string, uploadId: string): PersistedReadingEntry | null => {
  if (!uploadId) return null;
  return readStore()[readingStoreKey(ownerId, sessionId, uploadId)] ?? null;
};

export const saveReadingEntry = (
  ownerId: string,
  sessionId: string,
  uploadId: string,
  documentName: string,
  state: PdfReadingState,
): void => {
  if (!uploadId) return;
  const normalized = normalizeReadingState(state);
  if (!normalized) return;
  const store = readStore();
  store[readingStoreKey(ownerId, sessionId, uploadId)] = {
    uploadId,
    documentName: documentName || '',
    page: normalized.page,
    scale: normalized.scale,
    updatedAt: new Date().toISOString(),
  };
  writeStore(store);
};

// latestReadingEntryForSession returns the most recently touched document of a
// session so a refresh can reopen it from its upload identity.
export const latestReadingEntryForSession = (ownerId: string, sessionId: string): PersistedReadingEntry | null => {
  const prefix = readingStoreKey(ownerId, sessionId, '');
  const entries = Object.entries(readStore())
    .filter(([key]) => key.startsWith(prefix))
    .map(([, entry]) => entry);
  if (entries.length === 0) return null;
  return entries.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))[0];
};

export const clearReadingStore = (): void => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem(PDF_READING_STORAGE_KEY);
  } catch {
    // ignore
  }
};

type TranslationStore = Record<string, PersistedTranslationRecord>;

const readTranslationStore = (): TranslationStore => {
  if (typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(PDF_TRANSLATION_STORAGE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return {};
    const entries: TranslationStore = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (!value || typeof value !== 'object') continue;
      const candidate = value as Partial<PersistedTranslationRecord>;
      if (typeof candidate.uploadId !== 'string' || !candidate.uploadId.trim()) continue;
      if (typeof candidate.jobId !== 'string' || !candidate.jobId.trim()) continue;
      entries[key] = {
        uploadId: candidate.uploadId.trim(),
        jobId: candidate.jobId.trim(),
        status: typeof candidate.status === 'string' ? candidate.status : '',
        updatedAt: typeof candidate.updatedAt === 'string' ? candidate.updatedAt : '',
      };
    }
    return entries;
  } catch {
    return {};
  }
};

const writeTranslationStore = (store: TranslationStore) => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(PDF_TRANSLATION_STORAGE_KEY, JSON.stringify(store));
  } catch {
    // ignore
  }
};

export const readTranslationJobRecord = (
  ownerId: string,
  sessionId: string,
  uploadId: string,
): PersistedTranslationRecord | null => {
  if (!uploadId) return null;
  return readTranslationStore()[readingStoreKey(ownerId, sessionId, uploadId)] ?? null;
};

export const saveTranslationJobRecord = (
  ownerId: string,
  sessionId: string,
  uploadId: string,
  record: { jobId: string; status?: string },
): void => {
  if (!uploadId || !record.jobId) return;
  const store = readTranslationStore();
  store[readingStoreKey(ownerId, sessionId, uploadId)] = {
    uploadId,
    jobId: record.jobId,
    status: record.status || '',
    updatedAt: new Date().toISOString(),
  };
  writeTranslationStore(store);
};

export const clearTranslationJobRecord = (ownerId: string, sessionId: string, uploadId: string): void => {
  if (!uploadId) return;
  const store = readTranslationStore();
  const key = readingStoreKey(ownerId, sessionId, uploadId);
  if (!(key in store)) return;
  delete store[key];
  writeTranslationStore(store);
};

export const clearTranslationStore = (): void => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem(PDF_TRANSLATION_STORAGE_KEY);
  } catch {
    // ignore
  }
};
