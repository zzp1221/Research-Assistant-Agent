// The worker uses the exact version of the engine bundled by the adapter.
import { version } from 'pdfjs-dist';

export const PDF_WORKER_URL = `/pdfjs/${version}/pdf.worker.min.mjs`;
