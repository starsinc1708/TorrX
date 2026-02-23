import { useState } from 'react';
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  horizontalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { GripVertical, X, Plus } from 'lucide-react';
import { cn } from '../lib/cn';
import { Button } from './ui/button';

const COMMON_LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'ru', label: 'Russian' },
  { code: 'es', label: 'Spanish' },
  { code: 'fr', label: 'French' },
  { code: 'de', label: 'German' },
  { code: 'pt', label: 'Portuguese' },
  { code: 'it', label: 'Italian' },
  { code: 'zh', label: 'Chinese' },
  { code: 'ja', label: 'Japanese' },
  { code: 'ko', label: 'Korean' },
  { code: 'ar', label: 'Arabic' },
  { code: 'pl', label: 'Polish' },
  { code: 'nl', label: 'Dutch' },
  { code: 'tr', label: 'Turkish' },
  { code: 'uk', label: 'Ukrainian' },
];

interface SortableItemProps {
  id: string;
  onRemove: () => void;
  disabled?: boolean;
}

function SortableItem({ id, onRemove, disabled }: SortableItemProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id });
  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };
  const label = COMMON_LANGUAGES.find((l) => l.code === id)?.label ?? id;

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={cn(
        'flex items-center gap-1 rounded-md border bg-muted/50 px-2 py-1 text-sm',
        isDragging && 'opacity-50',
      )}
    >
      <button type="button" className="cursor-grab touch-none text-muted-foreground" {...attributes} {...listeners}>
        <GripVertical className="h-3 w-3" />
      </button>
      <span>{label}</span>
      <button
        type="button"
        onClick={onRemove}
        disabled={disabled}
        className="ml-1 text-muted-foreground hover:text-foreground"
      >
        <X className="h-3 w-3" />
      </button>
    </div>
  );
}

interface SortableLanguageListProps {
  languages: string[];
  onChange: (languages: string[]) => void;
  disabled?: boolean;
}

export function SortableLanguageList({ languages, onChange, disabled }: SortableLanguageListProps) {
  const [showDropdown, setShowDropdown] = useState(false);
  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;
    if (over && active.id !== over.id) {
      const oldIndex = languages.indexOf(String(active.id));
      const newIndex = languages.indexOf(String(over.id));
      onChange(arrayMove(languages, oldIndex, newIndex));
    }
  };

  const handleRemove = (code: string) => {
    onChange(languages.filter((l) => l !== code));
  };

  const handleAdd = (code: string) => {
    if (!languages.includes(code)) {
      onChange([...languages, code]);
    }
    setShowDropdown(false);
  };

  const available = COMMON_LANGUAGES.filter((l) => !languages.includes(l.code));

  return (
    <div className="space-y-2">
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={languages} strategy={horizontalListSortingStrategy}>
          <div className="flex flex-wrap gap-2">
            {languages.map((lang) => (
              <SortableItem key={lang} id={lang} onRemove={() => handleRemove(lang)} disabled={disabled} />
            ))}
          </div>
        </SortableContext>
      </DndContext>
      <div className="relative">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setShowDropdown(!showDropdown)}
          disabled={disabled || available.length === 0}
        >
          <Plus className="mr-1 h-3 w-3" />
          Add language
        </Button>
        {showDropdown && (
          <div className="ts-dropdown-panel absolute left-0 top-full z-50 mt-1 max-h-48 overflow-y-auto">
            {available.map((lang) => (
              <button
                key={lang.code}
                type="button"
                className="ts-dropdown-item w-full text-left"
                onClick={() => handleAdd(lang.code)}
              >
                {lang.label} ({lang.code})
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
