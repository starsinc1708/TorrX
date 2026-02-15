import React, { useEffect, useMemo, useState } from 'react';
import { X } from 'lucide-react';

import { cn } from '../lib/cn';
import { Input } from './ui/input';

const parseTagDraft = (raw: string): { committed: string[]; token: string } => {
  const parts = raw.split(',').map((t) => t.trim());
  const hasTrailingComma = raw.trimEnd().endsWith(',');
  if (hasTrailingComma) {
    return { committed: parts.filter(Boolean), token: '' };
  }
  const committed = parts.slice(0, -1).filter(Boolean);
  const token = (parts[parts.length - 1] ?? '').trim();
  return { committed, token };
};

const replaceLastToken = (raw: string, nextToken: string) => {
  const parts = raw.split(',');
  if (parts.length === 0) return `${nextToken}, `;
  parts[parts.length - 1] = ` ${nextToken}`;
  const joined = parts.join(',').replace(/^ /, '');
  return joined.trimEnd().endsWith(',') ? `${joined} ` : `${joined}, `;
};

type TagInputProps = {
  value: string;
  onChange: (value: string) => void;
  allTags: string[];
  placeholder?: string;
  className?: string;
  inputClassName?: string;
};

export default function TagInput({ value, onChange, allTags, placeholder, className, inputClassName }: TagInputProps) {
  const [draft, setDraft] = useState(value);
  const [focused, setFocused] = useState(false);

  useEffect(() => {
    if (value !== draft) setDraft(value);
  }, [value, draft]);

  const { committed: selectedTags, token: inputToken } = useMemo(() => parseTagDraft(draft), [draft]);
  const selected = useMemo(() => new Set(selectedTags.map((t) => t.toLowerCase())), [selectedTags]);
  const lastToken = inputToken.toLowerCase();

  const suggestions = useMemo(() => {
    const q = lastToken;
    const items = allTags
      .map((t) => t.trim())
      .filter(Boolean)
      .filter((t) => !selected.has(t.toLowerCase()))
      .filter((t) => (q ? t.toLowerCase().includes(q) : true));
    return items.slice(0, 16);
  }, [allTags, lastToken, selected]);

  const commit = (next: string) => {
    setDraft(next);
    onChange(next);
  };

  const appendTag = (tag: string) => {
    const cleanTag = tag.trim();
    if (!cleanTag) return;
    if (selected.has(cleanTag.toLowerCase())) return;
    const nextTags = [...selectedTags, cleanTag];
    commit(`${nextTags.join(', ')}, `);
  };

  const removeTag = (tag: string) => {
    const nextTags = selectedTags.filter((t) => t.toLowerCase() !== tag.toLowerCase());
    const suffix = inputToken ? `${inputToken}` : '';
    const prefix = nextTags.join(', ');
    const nextValue = prefix && suffix ? `${prefix}, ${suffix}` : prefix || suffix;
    commit(nextValue);
  };

  const showSuggestions = focused && suggestions.length > 0;

  return (
    <div className={cn('space-y-2', className)}>
      <div className="relative">
        <Input
          className={cn('h-9', inputClassName)}
          value={draft}
          onChange={(e) => commit(e.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => {
            window.setTimeout(() => setFocused(false), 120);
          }}
          onKeyDown={(e) => {
            if (e.key !== 'Enter' && e.key !== ',' && e.key !== 'Tab') return;
            const token = inputToken.trim();
            if (!token) {
              if (e.key === ',') e.preventDefault();
              return;
            }
            e.preventDefault();
            appendTag(token);
          }}
          placeholder={placeholder}
        />

        {showSuggestions ? (
          <div className="ts-dropdown-panel absolute left-0 right-0 top-[calc(100%+0.375rem)] z-30 max-h-52 overflow-y-auto p-1">
            <div className="px-2.5 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
              Suggested tags
            </div>
            <div className="space-y-0.5">
              {suggestions.map((tag) => (
                <button
                  key={`tag-suggest-${tag}`}
                  type="button"
                  className="ts-dropdown-item w-full cursor-pointer justify-start text-xs"
                  onClick={() => {
                    const token = inputToken.trim();
                    if (token.length > 0) {
                      commit(replaceLastToken(draft, tag));
                      return;
                    }
                    appendTag(tag);
                  }}
                  title={`Add tag ${tag}`}
                >
                  #{tag}
                </button>
              ))}
            </div>
          </div>
        ) : null}
      </div>

      {selectedTags.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          {selectedTags.map((tag) => (
            <button
              key={`selected-tag-${tag}`}
              type="button"
              className="inline-flex items-center gap-1 rounded-full border border-border/70 bg-muted/15 px-2 py-1 text-xs text-foreground transition-colors hover:bg-muted/25"
              onClick={() => removeTag(tag)}
              title={`Remove tag ${tag}`}
            >
              <span>#{tag}</span>
              <X className="h-3 w-3 text-muted-foreground" />
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
