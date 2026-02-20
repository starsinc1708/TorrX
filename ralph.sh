#!/bin/bash
set -e

###############################################################################
# ralph.sh — Long-running agent loop for incremental task execution.
#
# Based on: "Effective harnesses for long-running agents" (Anthropic, Nov 2025)
#
# Key principles:
#   1. Each iteration = one task, one commit, clean state
#   2. Agent reads progress + git log to get bearings BEFORE coding
#   3. Agent verifies BEFORE and AFTER implementing
#   4. Agent writes structured progress notes for the next session
#   5. tasks.json status fields are the source of truth
###############################################################################

TASKS_FILE="tasks.json"
PROGRESS_FILE="progress.txt"
MAX_ITERATIONS="${RALPH_MAX_ITERATIONS:-100}"
LOG_DIR="ralph-logs"
STATUS_FILE="ralph-logs/status.txt"
HEARTBEAT_FILE="ralph-logs/heartbeat"
ITERATION_TIMEOUT="${RALPH_TIMEOUT:-1800}"  # 30 min default per iteration

# Agent selection: RALPH_AGENT=claude|codex or auto-detect
resolve_agent() {
    if [[ -n "${RALPH_AGENT:-}" ]]; then
        echo "$RALPH_AGENT"
        return 0
    fi
    if command -v claude >/dev/null 2>&1; then
        echo "claude"
        return 0
    fi
    if command -v codex >/dev/null 2>&1; then
        echo "codex"
        return 0
    fi
    echo "ERROR: No supported agent found. Install 'claude' or 'codex', or set RALPH_AGENT." >&2
    return 1
}

run_agent() {
    local agent="$1"
    local prompt="$2"
    local log_file="$3"

    case "$agent" in
        claude)
            claude --permission-mode acceptEdits -p "$prompt" 2>&1 | tee "$log_file"
            ;;
        codex)
            local output_file
            output_file="$(mktemp -t ralph_codex.XXXXXX)"
            codex exec --full-auto --color never -C "$PWD" --output-last-message "$output_file" "$prompt" >/dev/null 2>&1
            cat "$output_file" | tee "$log_file"
            rm -f "$output_file"
            ;;
        *)
            echo "Unsupported agent: $agent" >&2
            return 1
            ;;
    esac
}

has_pending_tasks() {
    local count
    count=$(grep -c '"status": "pending"' "$TASKS_FILE" 2>/dev/null || echo "0")
    [ "$count" -gt 0 ]
}

# Write machine-readable status for external monitoring
write_status() {
    local state="$1"
    local detail="${2:-}"
    local pending done_n in_prog
    pending=$(count_by_status "pending")
    done_n=$(count_by_status "done")
    in_prog=$(count_by_status "in_progress")
    cat > "$STATUS_FILE" <<EOF
state=$state
iteration=$iteration
detail=$detail
pending=$pending
in_progress=$in_prog
done=$done_n
updated=$(date '+%Y-%m-%d %H:%M:%S')
pid=$$
EOF
    # heartbeat — just touch a file so external tools can check mtime
    touch "$HEARTBEAT_FILE"
}

# Run agent with timeout; kill if stuck
run_agent_with_timeout() {
    local agent="$1"
    local prompt="$2"
    local log_file="$3"

    write_status "running" "agent=$agent"

    # Background heartbeat: touch file every 60s while agent runs
    (
        while true; do
            sleep 60
            touch "$HEARTBEAT_FILE" 2>/dev/null || break
        done
    ) &
    local heartbeat_pid=$!

    local exit_code=0
    if [[ "$ITERATION_TIMEOUT" -gt 0 ]]; then
        timeout "$ITERATION_TIMEOUT" bash -c "$(declare -f run_agent); run_agent '$agent' \"\$1\" \"\$2\"" _ "$prompt" "$log_file" || exit_code=$?
    else
        run_agent "$agent" "$prompt" "$log_file" || exit_code=$?
    fi

    kill "$heartbeat_pid" 2>/dev/null || true
    wait "$heartbeat_pid" 2>/dev/null || true

    if [ "$exit_code" -eq 124 ]; then
        echo ""
        echo "⏰ TIMEOUT: Agent exceeded ${ITERATION_TIMEOUT}s limit"
        echo "timeout" >> "$log_file"
        write_status "timeout"
        return 124
    fi
    return "$exit_code"
}

count_by_status() {
    local status="$1"
    grep -c "\"status\": \"$status\"" "$TASKS_FILE" 2>/dev/null || echo "0"
}

# ── Initialization ───────────────────────────────────────────────────────────

mkdir -p "$LOG_DIR"

if [[ ! -f "$TASKS_FILE" ]]; then
    echo "ERROR: $TASKS_FILE not found. Run the prd-to-tasks agent first." >&2
    exit 1
fi

if [[ ! -f "$PROGRESS_FILE" ]]; then
    cat > "$PROGRESS_FILE" <<'INIT'
# Progress Log — T◎RRX

Each iteration appends a structured entry.
The coding agent reads this file at the start of every session to get context.

---
INIT
    echo "Created $PROGRESS_FILE"
fi

agent=$(resolve_agent) || exit 1
echo "Agent: $agent"
echo "Tasks file: $TASKS_FILE"
echo "Max iterations: $MAX_ITERATIONS"
echo "Iteration timeout: ${ITERATION_TIMEOUT}s"
echo ""
echo "Monitor: cat $STATUS_FILE"
echo "Heartbeat: stat $HEARTBEAT_FILE"
echo ""

# ── Main Loop ────────────────────────────────────────────────────────────────

iteration=1

while has_pending_tasks; do
    if [ "$iteration" -gt "$MAX_ITERATIONS" ]; then
        echo "Reached max iterations ($MAX_ITERATIONS). Stopping."
        break
    fi

    pending=$(count_by_status "pending")
    in_progress=$(count_by_status "in_progress")
    done_count=$(count_by_status "done")
    timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    log_file="$LOG_DIR/iteration-${iteration}-$(date '+%Y%m%d-%H%M%S').log"

    echo ""
    echo "═══════════════════════════════════════════════════════════════"
    echo "  Iteration $iteration  |  $timestamp"
    echo "  pending: $pending  |  in_progress: $in_progress  |  done: $done_count"
    echo "═══════════════════════════════════════════════════════════════"
    echo ""

    # ── Build the prompt ─────────────────────────────────────────────────

    prompt=$(cat <<'PROMPT'
You are a coding agent working on the T◎RRX project — a self-hosted torrent streaming platform.
You operate in a loop: each session you complete ONE task, then exit.

═══════════════════════════════════════════════════════════════════
PHASE 1 — GET YOUR BEARINGS (do this EVERY session, BEFORE coding)
═══════════════════════════════════════════════════════════════════

1. Run `pwd` to see your working directory.
2. Read `CLAUDE.md` for project conventions, build commands, and architecture.
3. Read `progress.txt` to see what previous sessions accomplished.
4. Run `git log --oneline -15` to see recent commits.
5. Read `tasks.json` — scan ALL tasks, note which are "done", "in_progress", "pending".

═══════════════════════════════════════════════════════════════════
PHASE 2 — PICK ONE TASK
═══════════════════════════════════════════════════════════════════

Choose the highest-priority task that is:
  - status: "pending" (not "done" or "in_progress")
  - all depends_on tasks are already "done"
  - priority order: critical > high > medium > low
  - within same priority: lower task ID first (T-001 before T-002)

Set its status to "in_progress" in tasks.json immediately.

═══════════════════════════════════════════════════════════════════
PHASE 2.5 — CHECK IF ALREADY IMPLEMENTED
═══════════════════════════════════════════════════════════════════

This codebase may ALREADY have working implementations for many tasks.
Before writing any code, CHECK whether the task is already done:

1. Read the files listed in files_likely_touched — do they already exist with relevant code?
2. If the files exist, examine the implementation:
   - Does it match the task description?
   - Does it satisfy the acceptance_criteria?
3. Run the verification_steps against the EXISTING code.

If the task is ALREADY IMPLEMENTED and passes verification:
  - Set status to "done"
  - In the "notes" field write a DETAILED description of how it was implemented:
    * Which files contain the implementation
    * Key design decisions (patterns used, data structures, algorithms)
    * How it integrates with the rest of the system
    * Any deviations from the task description (and why they make sense)
    * Lines of code / complexity estimate
  - This documentation is valuable — future agents use it to understand the codebase
  - Proceed to PHASE 6 (skip PHASE 3-5)

If the task is PARTIALLY implemented:
  - Note what exists and what's missing in "notes"
  - Only implement the MISSING parts in PHASE 4
  - Do not rewrite working code

If the task is NOT implemented at all:
  - Proceed to PHASE 3 normally

═══════════════════════════════════════════════════════════════════
PHASE 3 — VERIFY BEFORE CODING
═══════════════════════════════════════════════════════════════════

Before writing ANY code, run a basic health check:
  - For torrent-engine tasks: `cd services/torrent-engine && go build ./...`
  - For torrent-search tasks: `cd services/torrent-search && go build ./...`
  - For frontend tasks: `cd frontend && npx tsc --noEmit` (if package.json exists)
  - For infrastructure tasks: `docker compose -f deploy/docker-compose.yml config` (if exists)

If the build is broken, FIX IT FIRST before starting new work.
Log what you fixed in the notes field of the relevant task.

═══════════════════════════════════════════════════════════════════
PHASE 4 — IMPLEMENT
═══════════════════════════════════════════════════════════════════

Implement the task following the description and acceptance_criteria in tasks.json.
Consult the files_likely_touched list for guidance on where to put code.

Rules:
  - Follow conventions from CLAUDE.md (hexagonal architecture, Tailwind-first, etc.)
  - Write clean, tested, production-quality code
  - Do NOT modify other tasks' descriptions, verification_steps, or acceptance_criteria
  - Do NOT work on any other task — ONE task per session
  - If you discover a bug in existing code, fix it and note it in progress.txt

═══════════════════════════════════════════════════════════════════
PHASE 5 — VERIFY AFTER CODING
═══════════════════════════════════════════════════════════════════

Run the verification_steps from the task. Execute them literally:
  - Go tasks: `go build ./...`, `go vet ./...`, `go test ./...`
  - Frontend tasks: `npx tsc --noEmit`, `npm run build`
  - API tasks: use curl or go test to verify endpoints
  - Check each acceptance_criterion — does it pass? Be honest.

If something fails:
  - Fix it and re-verify
  - If you cannot fix it in this session, leave status as "in_progress"
  - Document what's broken in the task's notes field

Only mark status "done" when ALL acceptance criteria are met.

═══════════════════════════════════════════════════════════════════
PHASE 6 — CLEAN UP AND COMMIT
═══════════════════════════════════════════════════════════════════

1. Update tasks.json:
   - Set current task status to "done" (if all criteria passed) or leave "in_progress"
   - Update the "notes" field with what you did
   - Update meta.completedTasks count

2. Append to progress.txt (use this exact format):
   ```
   ## Iteration [N] — [YYYY-MM-DD HH:MM]
   **Task:** [T-XXX] [title]
   **Status:** done | in_progress | blocked
   **What was done:**
   - ...
   **Issues found:**
   - ... (or "None")
   **Notes for next session:**
   - ...
   ```

3. Git commit with a descriptive message:
   ```
   git add -A
   git commit -m "[T-XXX] <short description of what was implemented>"
   ```

═══════════════════════════════════════════════════════════════════
CRITICAL RULES
═══════════════════════════════════════════════════════════════════

- Work on EXACTLY ONE task per session
- NEVER edit task descriptions, verification_steps, or acceptance_criteria in tasks.json
- ONLY change: status, notes, meta.completedTasks
- NEVER mark a task "done" if tests fail or acceptance criteria are not met
- If the codebase is in a broken state, fix it BEFORE starting new work
- Leave the codebase in a clean, buildable state when you exit
- It is unacceptable to remove or edit acceptance criteria — they are immutable

When the task is complete and committed, output exactly:
<promise>COMPLETE</promise>

If the task is not complete (blocked or needs more work), output exactly:
<promise>IN_PROGRESS</promise>
PROMPT
)

    # ── Run the agent ────────────────────────────────────────────────────

    start_time=$(date +%s)
    result=$(run_agent_with_timeout "$agent" "$prompt" "$log_file")
    agent_exit=$?
    end_time=$(date +%s)
    elapsed=$(( end_time - start_time ))
    elapsed_min=$(( elapsed / 60 ))
    elapsed_sec=$(( elapsed % 60 ))

    echo ""
    echo "  Duration: ${elapsed_min}m ${elapsed_sec}s"

    # ── Process result ───────────────────────────────────────────────────

    if [ "$agent_exit" -eq 124 ]; then
        echo "⏰ Iteration timed out after ${ITERATION_TIMEOUT}s. Moving on."
        write_status "timeout" "iteration=$iteration duration=${elapsed}s"

    elif [[ "$result" == *"<promise>COMPLETE</promise>"* ]]; then
        echo "✅ Task completed in iteration $iteration"
        write_status "completed" "iteration=$iteration duration=${elapsed}s"

        remaining=$(count_by_status "pending")
        if [ "$remaining" -eq 0 ]; then
            echo ""
            echo "All tasks completed after $iteration iterations!"
            write_status "all_done"
            command -v say >/dev/null 2>&1 && say -v Milena "Хозяин, все задачи выполнены!" || true
            exit 0
        fi
        echo "Remaining: $remaining pending tasks. Continuing..."
        command -v say >/dev/null 2>&1 && say -v Milena "Задача готова. Продолжаю." || true

    elif [[ "$result" == *"<promise>IN_PROGRESS</promise>"* ]]; then
        echo "⏳ Task not finished — will retry next iteration"
        echo "   Check $log_file for details"
        write_status "in_progress" "iteration=$iteration duration=${elapsed}s"

    else
        echo "⚠️  Agent exited without promise tag. Check $log_file"
        write_status "unknown_exit" "iteration=$iteration duration=${elapsed}s"
    fi

    ((iteration++))
done

total_done=$(count_by_status "done")
echo ""
echo "Loop finished. Iterations: $((iteration-1)), Tasks done: $total_done"
write_status "finished" "iterations=$((iteration-1)) tasks_done=$total_done"
command -v say >/dev/null 2>&1 && say -v Milena "Цикл завершён." || true
