import { Loader2 } from 'lucide-react';

import { Card, CardContent } from './ui/card';

type RouteLoaderProps = {
  label?: string;
};

export default function RouteLoader({ label = 'Loading...' }: RouteLoaderProps) {
  return (
    <div className="grid min-h-[60vh] place-items-center animate-[ts-fade-in_200ms_ease-out] motion-reduce:animate-none">
      <Card className="w-full max-w-md">
        <CardContent className="flex items-center gap-3 py-6">
          <span className="grid h-10 w-10 place-items-center rounded-full bg-muted">
            <Loader2 size={18} className="animate-spin text-primary motion-reduce:animate-none" />
          </span>
          <div className="min-w-0">
            <div className="text-sm font-semibold">{label}</div>
            <div className="text-xs text-muted-foreground">Preparing the page</div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
