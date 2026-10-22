import type { UploadedFile } from '../../contracts/api';

export type PdfAttachmentLike = Pick<UploadedFile, 'name' | 'content_type'>;

export const isPdfAttachment = (attachment: PdfAttachmentLike): boolean => {
  const contentType = String(attachment.content_type || '').toLowerCase();
  const name = String(attachment.name || '').toLowerCase();
  return contentType === 'application/pdf' || name.endsWith('.pdf');
};

export const resolveAttachmentContentUrl = (apiBaseUrl: string, contentUrl: string): string => {
  const raw = String(contentUrl || '').trim();
  if (!raw) return '';
  if (/^https?:\/\//i.test(raw)) return raw;
  const base = apiBaseUrl.replace(/\/+$/, '');
  return `${base}${raw.startsWith('/') ? '' : '/'}${raw}`;
};
