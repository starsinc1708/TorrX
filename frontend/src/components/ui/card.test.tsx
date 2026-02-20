import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from './card';

describe('Card', () => {
  it('renders children', () => {
    render(<Card>Card body</Card>);
    expect(screen.getByText('Card body')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(<Card className="test-class" data-testid="card">Content</Card>);
    expect(screen.getByTestId('card')).toHaveClass('test-class');
  });
});

describe('CardHeader', () => {
  it('renders children', () => {
    render(<CardHeader>Header</CardHeader>);
    expect(screen.getByText('Header')).toBeInTheDocument();
  });
});

describe('CardTitle', () => {
  it('renders as h3', () => {
    render(<CardTitle>Title</CardTitle>);
    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Title');
  });
});

describe('CardDescription', () => {
  it('renders description text', () => {
    render(<CardDescription>Some description</CardDescription>);
    expect(screen.getByText('Some description')).toBeInTheDocument();
  });
});

describe('CardContent', () => {
  it('renders children', () => {
    render(<CardContent>Body content</CardContent>);
    expect(screen.getByText('Body content')).toBeInTheDocument();
  });
});

describe('CardFooter', () => {
  it('renders children', () => {
    render(<CardFooter>Footer</CardFooter>);
    expect(screen.getByText('Footer')).toBeInTheDocument();
  });
});

describe('Card composition', () => {
  it('renders full card with all sub-components', () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>My Card</CardTitle>
          <CardDescription>Card info</CardDescription>
        </CardHeader>
        <CardContent>Main content</CardContent>
        <CardFooter>Actions</CardFooter>
      </Card>,
    );
    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('My Card');
    expect(screen.getByText('Card info')).toBeInTheDocument();
    expect(screen.getByText('Main content')).toBeInTheDocument();
    expect(screen.getByText('Actions')).toBeInTheDocument();
  });
});
