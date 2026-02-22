# Production Readiness Implementation Plan (Playback Reliability)

**Date:** 2026-02-22  
**Related design docs:**
- `docs/plans/2026-02-21-unified-metrics-priority-ux-observability-design.md`
- `docs/plans/2026-02-22-production-readiness-design.md`

## Goal

Свести к нулю расхождения прогресса и сделать состояние плеера наблюдаемым:
- backend = единственный источник истины для progress/pieces/file-priority;
- `prioritizeActiveFileOnly` прозрачно подтверждается в UI;
- seek/startup/verify имеют метрики, дашборды и алерты.

## Scope

Три потока работ:
1. Unified Metrics Model (Pieces/File Pieces)
2. Priority UX Transparency (`prioritizeActiveFileOnly`)
3. Player Observability (metrics + alerts + dashboards)

Вне scope:
- редизайн UI и новые пользовательские фичи вне плеера;
- изменения модели авторизации/безопасности.

---

## Stage 0 - Baseline and Contract Freeze

### Tasks
- Зафиксировать текущие API/WS payload для `SessionState` и `FileRef`.
- Определить canonical поля (backend-owned):
  - torrent: `progress`, `transferPhase`, `verificationProgress`, `updatedAt`
  - file: `progress`, `priority`, `bytesCompleted`, `pieceStart`, `pieceEnd`
- Обновить API docs (`openapi.json`, `api.md`) под canonical-модель.

### Definition of Done
- Документирован contract v1 для прогресса и приоритетов.
- Убраны двусмысленные формулы в документации.
- Есть список мест во frontend, где есть локальные пересчеты (для удаления на Stage 2).

### Frontend Recalculation Hotspots (audit snapshot)
- `frontend/src/components/TorrentList.tsx:183` - fallback `state?.progress ?? normalizeProgress(torrent)`.
- `frontend/src/components/TorrentDetails.tsx:126` - fallback `sessionState?.progress ?? normalizeProgress(torrent)`.
- `frontend/src/components/PlayerFilesPanel.tsx:149` - локальный расчёт `filePieces = pieces.slice(start, end)`.
- `frontend/src/components/PlayerFilesPanel.tsx:158` - fallback `fileForPieces.progress ?? completedFilePieces/totalFilePieces`.
- `frontend/src/hooks/useVideoPlayer.ts:256` - fallback `Math.max(liveFile?.bytesCompleted, selectedFile?.bytesCompleted)` для file completion.

---

## Stage 1 - Backend Unified Metrics Source of Truth

### Tasks
- В `FileRef` обеспечить обязательную выдачу:
  - `progress` (piece-based 0..1),
  - `priority` (`none|low|normal|high|now`).
- На уровне engine/state унифицировать расчет:
  - один алгоритм для progress torrent/file,
  - одинаковая семантика для REST и WS.
- Добавить contract tests:
  - одинаковые значения в REST и WS для одного snapshot;
  - монотонность `verificationProgress` в фазе `verifying`;
  - отсутствие регрессии progress при рестарте с verify.

### Definition of Done
- UI получает одинаковые метрики из REST и WS без локальных fallback-формул.
- Прогресс не "скачет" при переключении источника данных.
- Все backend тесты и contract tests проходят.

---

## Stage 2 - Priority UX Transparency

### Tasks
- UI:
  - удалить локальные пересчеты `progress` и перейти на backend поля;
  - отрисовать per-file priority badge/indicator в PlayerFilesPanel и TorrentDetails;
  - явно показывать состояние режима focus (`prioritizeActiveFileOnly`) и фактические file priorities.
- Добавить UI/E2E сценарии:
  - включение/выключение режима;
  - проверка соседних файлов на `none/low`;
  - проверка смены active file.

### Definition of Done
- Пользователь видит не только toggle, но и факт применения приоритета к файлам.
- В UI нет кода, который пересчитывает progress из pieces локально.
- E2E подтверждает поведение на focus/unfocus и episode switch.

---

## Stage 3 - Player Observability (Prometheus + Grafana + Alerts)

### Metrics to Add
- `engine_hls_seek_total{result,mode}`
- `engine_hls_seek_latency_seconds` (histogram)
- `engine_hls_ttff_seconds` (time to first frame, histogram)
- `engine_hls_prebuffer_duration_seconds` (histogram)
- `engine_verify_duration_seconds` (histogram)
- `engine_focus_priority_mismatch_total` (counter)

### Tasks
- Instrumentation in backend lifecycle points:
  - seek request -> seek ready/error;
  - loading -> playlist ready (TTFF/prebuffer);
  - verifying enter -> verifying exit.
- Recording rules:
  - seek success rate,
  - P95 seek latency,
  - P95 TTFF,
  - P95 verify duration.
- Alerting rules:
  - low seek success rate,
  - high P95 seek latency,
  - high P95 TTFF,
  - long verify duration,
  - focus priority mismatch spike.
- Grafana row `Player SLO` with key panels.

### Definition of Done
- Есть дашборд с SLO-метриками плеера.
- Алерты срабатывают в тестовых инъекциях деградации.
- On-call может диагностировать проблему без воспроизведения на клиенте.

---

## Stage 4 - Hardening and Release Gate

### Tasks
- End-to-end regression set:
  - delete with files,
  - seek,
  - episode select,
  - resume,
  - restart + verify.
- Проверка документации:
  - `openapi.json` и `api.md` соответствуют фактическим endpoint и payload.
- Release checklist:
  - dashboards imported,
  - alert rules loaded,
  - smoke on staging.

### Definition of Done
- Критичный e2e-набор зеленый.
- Observability stack показывает стабильные значения на smoke.
- Решение готово к production rollout.

---

## KPI / Success Criteria

- Progress mismatch incidents: `0`.
- Seek success rate (5m window): `>= 99%`.
- P95 seek latency: `< 5s` (target).
- P95 TTFF: `< 15s` (target).
- Verify duration P95 within agreed baseline per content profile.
- User reports "focus mode not working": заметное снижение после релиза.

---

## Risks and Mitigations

- Риск: разные семантики progress в старых местах backend.
  - Mitigation: contract tests REST vs WS + единый helper расчета.
- Риск: UI зависит от старых fallback-формул.
  - Mitigation: staged migration + feature flag на переход (при необходимости).
- Риск: шумные алерты.
  - Mitigation: recording rules + `for` windows + baseline tuning на staging.

---

## Suggested Delivery Sequence

1. Stage 0 + Stage 1 (контракт и backend source of truth)
2. Stage 2 (UI прозрачность приоритета)
3. Stage 3 (метрики/алерты/дашборды)
4. Stage 4 (hardening + release gate)

---

## Verification Commands (Reference)

```bash
# Backend tests
cd services/torrent-engine
go test ./...

# Focused API tests
go test ./internal/api/http -count=1

# Validate OpenAPI JSON
cat services/torrent-engine/docs/openapi.json | jq . > /dev/null

# (Optional) check Prometheus rules
promtool check rules deploy/prometheus/rules/*.yml
```
