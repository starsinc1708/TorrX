import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Select } from './select';

describe('Select', () => {
  it('renders a select element with options', () => {
    render(
      <Select data-testid="sel">
        <option value="a">Alpha</option>
        <option value="b">Beta</option>
      </Select>,
    );
    const sel = screen.getByTestId('sel');
    expect(sel).toBeInTheDocument();
    expect(sel.tagName).toBe('SELECT');
  });

  it('selects default value', () => {
    render(
      <Select defaultValue="b" data-testid="sel">
        <option value="a">Alpha</option>
        <option value="b">Beta</option>
      </Select>,
    );
    expect(screen.getByTestId('sel')).toHaveValue('b');
  });

  it('fires onChange on selection', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Select onChange={onChange} data-testid="sel">
        <option value="a">Alpha</option>
        <option value="b">Beta</option>
      </Select>,
    );
    await user.selectOptions(screen.getByTestId('sel'), 'b');
    expect(onChange).toHaveBeenCalled();
  });

  it('can be disabled', () => {
    render(
      <Select disabled data-testid="sel">
        <option value="a">Alpha</option>
      </Select>,
    );
    expect(screen.getByTestId('sel')).toBeDisabled();
  });

  it('applies custom className', () => {
    render(
      <Select className="custom" data-testid="sel">
        <option value="a">Alpha</option>
      </Select>,
    );
    expect(screen.getByTestId('sel')).toHaveClass('custom');
  });
});
