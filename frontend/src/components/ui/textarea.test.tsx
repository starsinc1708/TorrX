import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Textarea } from './textarea';

describe('Textarea', () => {
  it('renders a textarea element', () => {
    render(<Textarea placeholder="Enter text" />);
    expect(screen.getByPlaceholderText('Enter text')).toBeInTheDocument();
  });

  it('accepts typed text', async () => {
    const user = userEvent.setup();
    render(<Textarea data-testid="ta" />);
    const ta = screen.getByTestId('ta');
    await user.type(ta, 'hello world');
    expect(ta).toHaveValue('hello world');
  });

  it('fires onChange', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Textarea onChange={onChange} data-testid="ta" />);
    await user.type(screen.getByTestId('ta'), 'x');
    expect(onChange).toHaveBeenCalled();
  });

  it('can be disabled', () => {
    render(<Textarea disabled data-testid="ta" />);
    expect(screen.getByTestId('ta')).toBeDisabled();
  });

  it('applies custom className', () => {
    render(<Textarea className="custom" data-testid="ta" />);
    expect(screen.getByTestId('ta')).toHaveClass('custom');
  });

  it('forwards ref', () => {
    const ref = { current: null as HTMLTextAreaElement | null };
    render(<Textarea ref={ref} />);
    expect(ref.current).toBeInstanceOf(HTMLTextAreaElement);
  });
});
