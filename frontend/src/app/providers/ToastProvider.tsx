import React, { createContext, useCallback, useContext, useMemo, useState } from 'react';
import { X } from 'lucide-react';

import { cn } from '../../lib/cn';

type ToastVariant = 'default' | 'success' | 'warning' | 'danger';

export type ToastInput = {
  title: string;
  description?: string;
  variant?: ToastVariant;
  durationMs?: number;
};

type ToastItem = ToastInput & {
  id: string;
  createdAt: number;
};

type ToastContextValue = {
  toast: (input: ToastInput) => void;
  dismiss: (id: string) => void;
  clear: () => void;
};

const ToastContext = createContext<ToastContextValue | null>(null);

const variantStyles: Record<ToastVariant, string> = {
  default: 'border-border/70 bg-card text-foreground',
  success: 'border-emerald-500/30 bg-emerald-500/10 text-foreground',
  warning: 'border-amber-500/30 bg-amber-500/10 text-foreground',
  danger: 'border-destructive/30 bg-destructive/10 text-foreground',
};

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);

  const dismiss = useCallback((id: string) => {
    setItems((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const clear = useCallback(() => setItems([]), []);

  const toast = useCallback(
    (input: ToastInput) => {
      const id = `${Date.now()}-${Math.random().toString(16).slice(2)}`;
      const item: ToastItem = {
        id,
        createdAt: Date.now(),
        variant: input.variant ?? 'default',
        title: input.title,
        description: input.description,
        durationMs: input.durationMs ?? 4500,
      };
      setItems((prev) => [item, ...prev].slice(0, 5));
      window.setTimeout(() => dismiss(id), item.durationMs);
    },
    [dismiss],
  );

  const value = useMemo<ToastContextValue>(() => ({ toast, dismiss, clear }), [toast, dismiss, clear]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        className="pointer-events-none fixed inset-0 z-[2000] flex flex-col items-end gap-2 p-4 sm:justify-end sm:p-6"
        role="region"
        aria-label="Notifications"
      >
        {items.map((t) => (
          <div
            key={t.id}
            className={cn(
              'pointer-events-auto w-[min(420px,calc(100vw-2rem))] overflow-hidden rounded-xl border shadow-[0_18px_50px_rgba(0,0,0,0.22)] ' +
                'backdrop-blur supports-[backdrop-filter]:bg-card/80 ' +
                'animate-[ts-fade-in_200ms_ease-out] motion-reduce:animate-none',
              variantStyles[t.variant ?? 'default'],
            )}
            role="status"
            aria-live="polite"
          >
            <div className="flex items-start gap-3 p-4">
              <div className="min-w-0 flex-1">
                <div className="text-sm font-semibold">{t.title}</div>
                {t.description ? (
                  <div className="mt-1 text-sm text-muted-foreground">{t.description}</div>
                ) : null}
              </div>
              <button
                type="button"
                className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                onClick={() => dismiss(t.id)}
                aria-label="Dismiss notification"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error('useToast must be used within ToastProvider');
  return ctx;
}

