export interface DesktopMetrics {
  goroutines: number;
  resident_memory_bytes: number;
  request_count: number;
  collected_at: string;
}

/** Reads the shell's local metrics endpoint (same-origin in the desktop app). */
export async function getDesktopMetrics(): Promise<DesktopMetrics> {
  const response = await fetch('/desktop/metrics', { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    throw new Error('metrics request failed with ' + response.status);
  }
  return (await response.json()) as DesktopMetrics;
}
