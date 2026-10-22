import assert from 'node:assert/strict';
import test from 'node:test';
import { isPdfAttachment, resolveAttachmentContentUrl } from '../src/features/pdf-viewer/pdfAttachment.ts';

test('detects PDF attachments by content type or extension', () => {
  assert.equal(isPdfAttachment({ name: 'paper.pdf', content_type: 'application/octet-stream' }), true);
  assert.equal(isPdfAttachment({ name: 'paper.bin', content_type: 'application/pdf' }), true);
  assert.equal(isPdfAttachment({ name: 'data.csv', content_type: 'text/csv' }), false);
});

test('resolves uploaded content URLs against the API base', () => {
  assert.equal(
    resolveAttachmentContentUrl('http://localhost:8080/', '/api/uploads/abc/content'),
    'http://localhost:8080/api/uploads/abc/content',
  );
  assert.equal(
    resolveAttachmentContentUrl('http://localhost:8080', 'api/uploads/abc/content'),
    'http://localhost:8080/api/uploads/abc/content',
  );
  assert.equal(
    resolveAttachmentContentUrl('http://localhost:8080', 'https://cdn.example/paper.pdf'),
    'https://cdn.example/paper.pdf',
  );
  assert.equal(resolveAttachmentContentUrl('http://localhost:8080', ''), '');
});
