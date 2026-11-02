import { useEffect, useState } from 'react';
import { events } from '../../services/mygo';

export interface UpdateAvailablePayload {
  version?: string;
  url?: string;
  sha256?: string;
  signature?: string;
  release_notes_url?: string;
}

interface WailsRuntime {
  EventsOn(event: string, callback: (...args: unknown[]) => void): (() => void) | void;
}

/**
 * Listens for the ra:update-available event. The Wails shell emits it through
 * its runtime, the MyGo shell through the typed event of the generated client.
 */
export function useUpdateAvailable() {
  const [update, setUpdate] = useState<UpdateAvailablePayload | null>(null);

  useEffect(() => {
    const runtime = (window as unknown as { runtime?: WailsRuntime }).runtime;
    if (runtime?.EventsOn) {
      const off = runtime.EventsOn('ra:update-available', (payload) => {
        if (payload && typeof payload === 'object') {
          setUpdate(payload as UpdateAvailablePayload);
        }
      });
      return () => {
        if (typeof off === 'function') off();
      };
    }
    if ((window as unknown as { mygo?: unknown }).mygo) {
      return events.raUpdateAvailable.on((payload) => {
        if (payload && typeof payload === 'object') {
          setUpdate(payload as UpdateAvailablePayload);
        }
      });
    }
  }, []);

  return { update, dismiss: () => setUpdate(null) };
}
