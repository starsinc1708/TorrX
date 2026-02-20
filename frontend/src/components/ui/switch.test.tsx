import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Switch } from './switch';

describe('Switch', () => {
  it('renders unchecked by default', () => {
    render(<Switch aria-label="toggle" />);
    const sw = screen.getByRole('switch');
    expect(sw).toBeInTheDocument();
    expect(sw).toHaveAttribute('data-state', 'unchecked');
  });

  it('renders checked when defaultChecked', () => {
    render(<Switch defaultChecked aria-label="toggle" />);
    expect(screen.getByRole('switch')).toHaveAttribute('data-state', 'checked');
  });

  it('toggles on click', async () => {
    const user = userEvent.setup();
    const onCheckedChange = vi.fn();
    render(<Switch onCheckedChange={onCheckedChange} aria-label="toggle" />);
    await user.click(screen.getByRole('switch'));
    expect(onCheckedChange).toHaveBeenCalledWith(true);
  });

  it('can be disabled', () => {
    render(<Switch disabled aria-label="toggle" />);
    expect(screen.getByRole('switch')).toBeDisabled();
  });

  it('applies custom className', () => {
    render(<Switch className="extra" aria-label="toggle" />);
    expect(screen.getByRole('switch')).toHaveClass('extra');
  });
});
