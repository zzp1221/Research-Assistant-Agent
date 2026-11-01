import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { DesktopStatusBanner } from '../../src/components/DesktopStatusBanner';

describe('DesktopStatusBanner', () => {
  it('shows a restart notice', () => {
    render(<DesktopStatusBanner state={{ state: 'running', restarts: 1, last_restart_at: '2026-10-31T00:00:00Z' }} />);
    expect(screen.getByRole('status')).toHaveTextContent('本地服务已重启');
  });

  it('shows the failure reason', () => {
    render(<DesktopStatusBanner state={{ state: 'failed', error: 'backend health check failed' }} />);
    expect(screen.getByRole('status')).toHaveTextContent('backend health check failed');
  });

  it('stays hidden for a healthy first run', () => {
    const { container } = render(<DesktopStatusBanner state={{ state: 'running', restarts: 0 }} />);
    expect(container).toBeEmptyDOMElement();
  });
});
