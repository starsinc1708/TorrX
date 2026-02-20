import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Badge } from './badge';

describe('Badge', () => {
  it('renders with text', () => {
    render(<Badge>New</Badge>);
    expect(screen.getByText('New')).toBeInTheDocument();
  });

  it('renders with default variant', () => {
    render(<Badge data-testid="badge">Default</Badge>);
    expect(screen.getByTestId('badge')).toBeInTheDocument();
  });

  it('renders with success variant', () => {
    render(<Badge variant="success" data-testid="badge">Done</Badge>);
    expect(screen.getByTestId('badge')).toHaveTextContent('Done');
  });

  it('renders with warning variant', () => {
    render(<Badge variant="warning">Pending</Badge>);
    expect(screen.getByText('Pending')).toBeInTheDocument();
  });

  it('renders with danger variant', () => {
    render(<Badge variant="danger">Error</Badge>);
    expect(screen.getByText('Error')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(<Badge className="extra" data-testid="badge">Styled</Badge>);
    expect(screen.getByTestId('badge')).toHaveClass('extra');
  });
});
