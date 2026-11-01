import { useEffect, useState } from 'react';

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

/** Listens for the Wails ra:update-available event emitted by main_wails. */
export function useUpdateAvailable() {
  const [update, setUpdate] = useState<UpdateAvailablePayload | null>(null);

  useEffect(() => {
    const runtime = (window as unknown as { runtime?: WailsRuntime }).runtime;
    if (!runtime?.EventsOn) return;
    const off = runtime.EventsOn('ra:update-available', (payload) => {
      if (payload && typeof payload === 'object') {
        setUpdate(payload as UpdateAvailablePayload);
      }
    });
    return () => {
      if (typeof off === 'function') off();
    };
  }, []);

  return { update, dismiss: () => setUpdate(null) };
}
