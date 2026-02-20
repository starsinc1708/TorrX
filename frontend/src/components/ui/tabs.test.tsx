import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Tabs, TabsList, TabsTrigger, TabsContent } from './tabs';

describe('Tabs', () => {
  it('renders tab triggers', () => {
    render(
      <Tabs defaultValue="one">
        <TabsList>
          <TabsTrigger value="one">Tab 1</TabsTrigger>
          <TabsTrigger value="two">Tab 2</TabsTrigger>
        </TabsList>
        <TabsContent value="one">Content 1</TabsContent>
        <TabsContent value="two">Content 2</TabsContent>
      </Tabs>,
    );
    expect(screen.getByRole('tab', { name: 'Tab 1' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Tab 2' })).toBeInTheDocument();
  });

  it('shows correct content for default tab', () => {
    render(
      <Tabs defaultValue="one">
        <TabsList>
          <TabsTrigger value="one">Tab 1</TabsTrigger>
          <TabsTrigger value="two">Tab 2</TabsTrigger>
        </TabsList>
        <TabsContent value="one">Content 1</TabsContent>
        <TabsContent value="two">Content 2</TabsContent>
      </Tabs>,
    );
    expect(screen.getByText('Content 1')).toBeInTheDocument();
  });

  it('switches content on tab click', async () => {
    const user = userEvent.setup();
    render(
      <Tabs defaultValue="one">
        <TabsList>
          <TabsTrigger value="one">Tab 1</TabsTrigger>
          <TabsTrigger value="two">Tab 2</TabsTrigger>
        </TabsList>
        <TabsContent value="one">Content 1</TabsContent>
        <TabsContent value="two">Content 2</TabsContent>
      </Tabs>,
    );
    await user.click(screen.getByRole('tab', { name: 'Tab 2' }));
    expect(screen.getByText('Content 2')).toBeInTheDocument();
  });

  it('applies custom className to TabsList', () => {
    render(
      <Tabs defaultValue="a">
        <TabsList className="custom-list" data-testid="list">
          <TabsTrigger value="a">A</TabsTrigger>
        </TabsList>
        <TabsContent value="a">A content</TabsContent>
      </Tabs>,
    );
    expect(screen.getByTestId('list')).toHaveClass('custom-list');
  });
});
