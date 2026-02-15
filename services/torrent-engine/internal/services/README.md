# Backend Services

This directory groups backend services by responsibility:

- `torrent/`: torrent runtime and media processing service.
  - `engine/`: torrent engine adapters (`anacrolix`, `ffprobe`).
- `session/`: user session state service.
  - `player/`: current player session manager.
  - `repository/mongo/`: MongoDB storage for player and watch history sessions.
- `search/`: torrent search service.
  - `parser/`: tracker parsing/search abstractions (scaffold for next implementation step).
