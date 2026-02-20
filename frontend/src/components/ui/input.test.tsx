import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Input } from './input';

describe('Input', () => {
  it('renders an input element', () => {
    render(<Input placeholder="Type here" />);
    expect(screen.getByPlaceholderText('Type here')).toBeInTheDocument();
  });

  it('accepts and displays typed text', async () => {
    const user = userEvent.setup();
    render(<Input data-testid="input" />);
    const input = screen.getByTestId('input');
    await user.type(input, 'hello');
    expect(input).toHaveValue('hello');
  });

  it('fires onChange', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Input onChange={onChange} data-testid="input" />);
    await user.type(screen.getByTestId('input'), 'a');
    expect(onChange).toHaveBeenCalled();
  });

  it('can be disabled', () => {
    render(<Input disabled data-testid="input" />);
    expect(screen.getByTestId('input')).toBeDisabled();
  });

  it('applies type attribute', () => {
    render(<Input type="password" data-testid="input" />);
    expect(screen.getByTestId('input')).toHaveAttribute('type', 'password');
  });

  it('applies custom className', () => {
    render(<Input className="custom" data-testid="input" />);
    expect(screen.getByTestId('input')).toHaveClass('custom');
  });

  it('forwards ref', () => {
    const ref = { current: null as HTMLInputElement | null };
    render(<Input ref={ref} />);
    expect(ref.current).toBeInstanceOf(HTMLInputElement);
  });
});
