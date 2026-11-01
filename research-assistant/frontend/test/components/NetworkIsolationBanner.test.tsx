import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { NetworkIsolationBanner } from '../../src/components/NetworkIsolationBanner';

describe('NetworkIsolationBanner', () => {
  it('warns in native lowil mode', () => {
    render(<NetworkIsolationBanner capabilities={{ provider: 'native', isolation: 'lowil', network_enforced: 'none' }} />);
    expect(screen.getByRole('status')).toHaveTextContent('网络隔离未由操作系统强制');
  });

  it('stays hidden when the OS enforces the network', () => {
    const { container } = render(<NetworkIsolationBanner capabilities={{ provider: 'docker', isolation: 'container', network_enforced: 'os' }} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('stays hidden for appcontainer native mode', () => {
    const { container } = render(<NetworkIsolationBanner capabilities={{ provider: 'native', isolation: 'appcontainer', network_enforced: 'os' }} />);
    expect(container).toBeEmptyDOMElement();
  });
});
