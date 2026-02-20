import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Alert, AlertTitle, AlertDescription } from './alert';

describe('Alert', () => {
  it('renders with role="alert"', () => {
    render(<Alert>Warning</Alert>);
    expect(screen.getByRole('alert')).toHaveTextContent('Warning');
  });

  it('applies custom className', () => {
    render(<Alert className="custom">Text</Alert>);
    expect(screen.getByRole('alert')).toHaveClass('custom');
  });
});

describe('AlertTitle', () => {
  it('renders title text', () => {
    render(<AlertTitle>Title</AlertTitle>);
    expect(screen.getByText('Title')).toBeInTheDocument();
  });
});

describe('AlertDescription', () => {
  it('renders description text', () => {
    render(<AlertDescription>Details here</AlertDescription>);
    expect(screen.getByText('Details here')).toBeInTheDocument();
  });
});

describe('Alert composition', () => {
  it('renders full alert with title and description', () => {
    render(
      <Alert>
        <AlertTitle>Error</AlertTitle>
        <AlertDescription>Something went wrong</AlertDescription>
      </Alert>,
    );
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByText('Error')).toBeInTheDocument();
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
  });
});
