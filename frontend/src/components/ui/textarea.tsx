import * as React from 'react';
import { cn } from '../../lib/cn';

export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {}

export const Textarea = React.forwardRef<HTMLTextAreaElement, TextareaProps>(({ className, ...props }, ref) => {
  return (
    <textarea
      ref={ref}
      className={cn(
        'flex min-h-[96px] w-full resize-y rounded-xl border border-border/70 bg-card/60 px-3 py-2 text-sm text-foreground shadow-soft ' +
          'transition-[background-color,border-color,color,box-shadow] duration-200 hover:bg-card ' +
          'placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 ring-offset-background ' +
          'disabled:cursor-not-allowed disabled:bg-muted/20 disabled:text-muted-foreground disabled:opacity-60',
        className,
      )}
      {...props}
    />
  );
});
Textarea.displayName = 'Textarea';
