import React, { useEffect, useMemo, useState } from 'react';
import { Link2, Upload } from 'lucide-react';
import { createTorrentFromFile, createTorrentFromMagnet, isApiError } from '../api';
import type { TorrentRecord } from '../types';
import { Button } from './ui/button';
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from './ui/dialog';
import { Input } from './ui/input';
import { Textarea } from './ui/textarea';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './ui/tabs';

interface AddTorrentModalProps {
  open: boolean;
  onClose: () => void;
  onCreated?: (torrent: TorrentRecord) => void | Promise<void>;
}

const AddTorrentModal: React.FC<AddTorrentModalProps> = ({ open, onClose, onCreated }) => {
  const [createMode, setCreateMode] = useState<'magnet' | 'file'>('magnet');
  const [magnetValue, setMagnetValue] = useState('');
  const [torrentFile, setTorrentFile] = useState<File | null>(null);
  const [torrentName, setTorrentName] = useState('');
  const [creating, setCreating] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setErrorMessage(null);
  }, [open, onClose]);

  const resetForm = () => {
    setMagnetValue('');
    setTorrentFile(null);
    setTorrentName('');
    setCreateMode('magnet');
    setErrorMessage(null);
  };

  const handleClose = () => {
    if (creating) return;
    resetForm();
    onClose();
  };

  const handleSubmit = async () => {
    if (creating) return;
    setCreating(true);
    try {
      const trimmedName = torrentName.trim();
      let record: TorrentRecord;
      if (createMode === 'magnet') {
        const magnet = magnetValue.trim();
        if (!magnet) {
          setErrorMessage('Magnet link is required');
          return;
        }
        record = await createTorrentFromMagnet(magnet, trimmedName || undefined);
      } else {
        if (!torrentFile) {
          setErrorMessage('Torrent file is required');
          return;
        }
        record = await createTorrentFromFile(torrentFile, trimmedName || undefined);
      }
      await onCreated?.(record);
      handleClose();
    } catch (error) {
      if (isApiError(error)) {
        setErrorMessage(`${error.code ?? 'error'}: ${error.message}`);
      } else if (error instanceof Error) {
        setErrorMessage(error.message);
      } else {
        setErrorMessage('Unexpected error');
      }
    } finally {
      setCreating(false);
    }
  };

  const filename = useMemo(() => (torrentFile ? torrentFile.name : 'Choose .torrent file'), [torrentFile]);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) handleClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add torrent</DialogTitle>
          <DialogDescription>Paste a magnet link or upload a .torrent file.</DialogDescription>
        </DialogHeader>

        <DialogBody>
          <Tabs value={createMode} onValueChange={(v) => setCreateMode(v as 'magnet' | 'file')}>
            <TabsList className="w-full">
              <TabsTrigger value="magnet" className="flex-1 gap-2">
                <Link2 className="h-4 w-4" />
                Magnet
              </TabsTrigger>
              <TabsTrigger value="file" className="flex-1 gap-2">
                <Upload className="h-4 w-4" />
                File
              </TabsTrigger>
            </TabsList>

            <TabsContent value="magnet" className="space-y-3">
              <div className="space-y-2">
                <div className="text-sm font-medium">Magnet link</div>
                <Textarea
                  value={magnetValue}
                  onChange={(event) => setMagnetValue(event.target.value)}
                  placeholder="magnet:?xt=urn:btih:..."
                  rows={4}
                />
              </div>
            </TabsContent>

            <TabsContent value="file" className="space-y-3">
              <div className="space-y-2">
                <div className="text-sm font-medium">Torrent file</div>
                <label className="group relative block cursor-pointer rounded-lg border border-dashed border-border/80 bg-muted/30 p-4 transition-colors hover:bg-muted/50">
                  <div className="flex items-center gap-3">
                    <span className="inline-flex h-10 w-10 items-center justify-center rounded-md bg-background shadow-sm">
                      <Upload className="h-4 w-4 text-primary" />
                    </span>
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium">{filename}</div>
                      <div className="text-xs text-muted-foreground">Click to choose, or drop a file here</div>
                    </div>
                  </div>
                  <input
                    className="absolute inset-0 cursor-pointer opacity-0"
                    type="file"
                    accept=".torrent"
                    onChange={(event) => setTorrentFile(event.target.files?.[0] ?? null)}
                  />
                </label>
              </div>
            </TabsContent>
          </Tabs>

          <div className="mt-4 space-y-2">
            <div className="text-sm font-medium">Name (optional)</div>
            <Input
              value={torrentName}
              onChange={(event) => setTorrentName(event.target.value)}
              placeholder="Display name"
            />
          </div>

          {errorMessage ? (
            <div className="mt-4 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-foreground">
              {errorMessage}
            </div>
          ) : null}
        </DialogBody>

        <DialogFooter>
          <Button variant="ghost" onClick={handleClose} disabled={creating}>
            Cancel
          </Button>
          <Button onClick={() => void handleSubmit()} disabled={creating}>
            {creating ? 'Creating…' : 'Add'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};

export default AddTorrentModal;
