import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { EnvironmentBadge } from '../../src/components/EnvironmentBadge';

describe('EnvironmentBadge', () => {
  it('renders container provider', () => {
    render(<EnvironmentBadge manifest={{ sandbox_provider: 'docker', isolation: 'container' }} />);
    expect(screen.getByText('容器')).toBeInTheDocument();
  });

  it('renders native compatibility mode honestly', () => {
    render(<EnvironmentBadge manifest={{ sandbox_provider: 'native', isolation: 'lowil', network_enforced: 'none' }} />);
    expect(screen.getByText('原生(兼容)')).toBeInTheDocument();
    expect(screen.getByTitle(/network: none/)).toBeInTheDocument();
  });

  it('renders nothing without a manifest', () => {
    const { container } = render(<EnvironmentBadge manifest={null} />);
    expect(container).toBeEmptyDOMElement();
  });
});
