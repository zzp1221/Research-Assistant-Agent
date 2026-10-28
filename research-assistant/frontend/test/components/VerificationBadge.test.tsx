import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { VerificationBadge, VerificationDetails } from '../../src/features/verification/VerificationBadge';
import type { Verification } from '../../src/contracts/api';

const base: Verification = {
  status: 'pass',
  level: 'visual',
  verified: true,
  confidence: 0.82,
  reason: 'all images passed the structural gate and the visual review',
  verified_by: { profile_id: 'p1', model: 'vision-model', mode: 'main' },
};

describe('VerificationBadge', () => {
  it('renders a pass badge for a verified image', () => {
    render(<VerificationBadge verification={base} />);
    const badge = screen.getByTestId('verification-badge');
    expect(badge.getAttribute('data-verification-status')).toBe('pass');
    expect(badge.textContent).toContain('校验通过');
  });

  it('never presents inconclusive as pass and offers the settings entry', () => {
    const onOpenSettings = vi.fn();
    render(
      <VerificationBadge
        verification={{ ...base, status: 'inconclusive', verified: false, level: 'structural', reason: 'no vision-capable model profile is configured; only structural and text checks ran' }}
        onOpenSettings={onOpenSettings}
      />,
    );
    const badge = screen.getByTestId('verification-badge');
    expect(badge.getAttribute('data-verification-status')).toBe('inconclusive');
    expect(badge.textContent).toContain('不可确认');
    expect(badge.textContent).not.toContain('校验通过');
    screen.getByText('去设置').click();
    expect(onOpenSettings).toHaveBeenCalled();
  });

  it('renders nothing when there is no verification (legacy plans)', () => {
    const { container } = render(<VerificationBadge verification={null} />);
    expect(container.firstChild).toBeNull();
  });
});

describe('VerificationDetails', () => {
  it('shows issues and the verifying model for a fail verdict', () => {
    render(
      <VerificationDetails
        verification={{
          ...base,
          status: 'fail',
          verified: false,
          level: 'visual',
          issues: ['x axis has no unit'],
          verified_by: { profile_id: 'p2', model: 'fallback-vision', mode: 'vision_fallback' },
        }}
      />,
    );
    expect(screen.getByText('x axis has no unit')).toBeTruthy();
    expect(screen.getByText(/fallback-vision/)).toBeTruthy();
    expect(screen.getByText('校验未通过 · visual')).toBeTruthy();
  });
});
