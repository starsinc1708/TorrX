import * as React from 'react';
import { Check, ChevronDown, X } from 'lucide-react';

import { cn } from '../../lib/cn';
import { Button } from './button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from './dropdown-menu';

export type MultiSelectOption = {
  value: string;
  label?: string;
  disabled?: boolean;
};

export type MultiSelectProps = {
  value: string[];
  options: MultiSelectOption[];
  onChange: (next: string[]) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  contentClassName?: string;
  label?: string;
};

const uniq = (items: string[]) => Array.from(new Set(items));

export function MultiSelect({
  value,
  options,
  onChange,
  placeholder = 'Any',
  disabled,
  className,
  contentClassName,
  label,
}: MultiSelectProps) {
  const selected = React.useMemo(() => new Set(value), [value]);

  const selectedLabel = React.useMemo(() => {
    if (value.length === 0) return placeholder;
    if (value.length <= 3) return value.join(', ');
    return `${value.slice(0, 2).join(', ')} +${value.length - 2}`;
  }, [value, placeholder]);

  const toggle = React.useCallback(
    (v: string) => {
      const next = new Set(value);
      if (next.has(v)) next.delete(v);
      else next.add(v);
      onChange(uniq(Array.from(next)));
    },
    [value, onChange],
  );

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild disabled={disabled}>
        <button
          type="button"
          disabled={disabled}
          className={cn(
            'ts-dropdown-trigger relative flex items-center justify-between gap-2 text-left',
            className,
          )}
          aria-label={label ?? 'Multi select'}
        >
          <span className={cn('min-w-0 flex-1 truncate', value.length === 0 ? 'text-muted-foreground' : '')}>
            {selectedLabel}
          </span>
          <span className="inline-flex items-center gap-2">
            {value.length > 0 ? (
              <span className="inline-flex h-6 items-center rounded-full border border-border/70 bg-muted/20 px-2 text-[11px] font-semibold text-muted-foreground">
                {value.length}
              </span>
            ) : null}
            <ChevronDown className="h-4 w-4 text-muted-foreground" />
          </span>
        </button>
      </DropdownMenuTrigger>

      <DropdownMenuContent align="start" className={cn('w-[min(360px,calc(100vw-2rem))]', contentClassName)}>
        <div className="flex items-center justify-between gap-2 px-2.5 py-2">
          <DropdownMenuLabel className="p-0">{label ?? 'Select'}</DropdownMenuLabel>
          <Button
            variant="ghost"
            size="sm"
            className="h-8 px-2"
            disabled={value.length === 0}
            onClick={() => onChange([])}
          >
            <X className="h-4 w-4" />
            Clear
          </Button>
        </div>
        <DropdownMenuSeparator />
        <div className="max-h-[min(56vh,420px)] overflow-y-auto overscroll-contain p-1">
          {options.length === 0 ? (
            <div className="px-2.5 py-2 text-sm text-muted-foreground">No options</div>
          ) : (
            options.map((opt) => {
              const v = opt.value;
              const active = selected.has(v);
              return (
                <DropdownMenuItem
                  key={v}
                  disabled={opt.disabled}
                  className={cn('cursor-pointer', active ? 'bg-accent text-accent-foreground' : '')}
                  onSelect={(e) => {
                    // Keep menu open while toggling multiple values.
                    e.preventDefault();
                    toggle(v);
                  }}
                >
                  <span className="inline-flex w-5 items-center justify-center">
                    {active ? <Check className="h-4 w-4 text-primary" /> : <span className="h-4 w-4" />}
                  </span>
                  <span className="flex-1 truncate">{opt.label ?? v}</span>
                </DropdownMenuItem>
              );
            })
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
