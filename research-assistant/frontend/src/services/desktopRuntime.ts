import type { DesktopRuntimeStatus } from '../contracts/desktop';

/** Reads the shell's native runtime cache status. */
export async function getDesktopRuntimeStatus(): Promise<DesktopRuntimeStatus> {
  const response = await fetch('/desktop/runtime', { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    throw new Error('runtime status request failed with ' + response.status);
  }
  return (await response.json()) as DesktopRuntimeStatus;
}
