$ErrorActionPreference = "Stop"

###############################################################################
# ralph.ps1 — Long-running agent loop for incremental task execution.
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

$TASKS_FILE    = "tasks.json"
$PROGRESS_FILE = "progress.txt"
$MAX_ITERATIONS     = if ($env:RALPH_MAX_ITERATIONS) { [int]$env:RALPH_MAX_ITERATIONS } else { 100 }
$LOG_DIR            = "ralph-logs"
$STATUS_FILE        = "ralph-logs/status.txt"
$HEARTBEAT_FILE     = "ralph-logs/heartbeat"
$ITERATION_TIMEOUT  = if ($env:RALPH_TIMEOUT) { [int]$env:RALPH_TIMEOUT } else { 1800 }  # 30 min default

# ── Helper Functions ────────────────────────────────────────────────────────

function Resolve-Agent {
    if ($env:RALPH_AGENT) { return $env:RALPH_AGENT }
    if (Get-Command claude -ErrorAction SilentlyContinue) { return "claude" }
    if (Get-Command codex  -ErrorAction SilentlyContinue) { return "codex" }
    return $null
}

function Invoke-Agent {
    param(
        [string]$Agent,
        [string]$Prompt,
        [string]$LogFile
    )
    switch ($Agent) {
        "claude" {
            claude --permission-mode acceptEdits -p $Prompt 2>&1 | Tee-Object -FilePath $LogFile
        }
        "codex" {
            $outputFile = [System.IO.Path]::GetTempFileName()
            codex exec --full-auto --color never -C $PWD --output-last-message $outputFile $Prompt | Out-Null
            $result = Get-Content $outputFile -Raw
            $result | Tee-Object -FilePath $LogFile
            Remove-Item $outputFile -Force -ErrorAction SilentlyContinue
        }
        default {
            Write-Error "Unsupported agent: $Agent"
        }
    }
}

function Get-StatusCount {
    param([string]$Status)
    if (-not (Test-Path $TASKS_FILE)) { return 0 }
    $content = Get-Content $TASKS_FILE -Raw
    return ([regex]::Matches($content, "`"status`":\s*`"$Status`"")).Count
}

function Write-Status {
    param(
        [string]$State,
        [string]$Detail = ""
    )
    $pending = Get-StatusCount "pending"
    $doneN   = Get-StatusCount "done"
    $inProg  = Get-StatusCount "in_progress"
    $ts      = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    @"
state=$State
iteration=$script:iteration
detail=$Detail
pending=$pending
in_progress=$inProg
done=$doneN
updated=$ts
pid=$PID
"@ | Set-Content -Path $STATUS_FILE -Encoding UTF8

    # heartbeat — touch file so external tools can check LastWriteTime
    if (Test-Path $HEARTBEAT_FILE) {
        (Get-Item $HEARTBEAT_FILE).LastWriteTime = Get-Date
    } else {
        New-Item -Path $HEARTBEAT_FILE -ItemType File -Force | Out-Null
    }
}

function Invoke-AgentWithTimeout {
    param(
        [string]$Agent,
        [string]$Prompt,
        [string]$LogFile
    )

    Write-Status "running" "agent=$Agent"

    # Background heartbeat job: touch file every 60s
    $hbPath = if (Test-Path $HEARTBEAT_FILE) { (Resolve-Path $HEARTBEAT_FILE).Path } else { (Join-Path $PWD $HEARTBEAT_FILE) }
    $heartbeatJob = Start-Job -ScriptBlock {
        param($file)
        while ($true) {
            Start-Sleep -Seconds 60
            if (Test-Path $file) {
                (Get-Item $file).LastWriteTime = Get-Date
            } else {
                New-Item -Path $file -ItemType File -Force | Out-Null
            }
        }
    } -ArgumentList $hbPath

    # Run agent as a job for timeout support
    $agentJob = Start-Job -ScriptBlock {
        param($agent, $prompt, $logFile, $pwd)
        Set-Location $pwd
        switch ($agent) {
            "claude" {
                $output = claude --permission-mode acceptEdits -p $prompt 2>&1
                $output | Out-File -FilePath $logFile -Encoding UTF8
                $output
            }
            "codex" {
                $outputFile = [System.IO.Path]::GetTempFileName()
                codex exec --full-auto --color never -C $pwd --output-last-message $outputFile $prompt | Out-Null
                $result = Get-Content $outputFile -Raw
                $result | Out-File -FilePath $logFile -Encoding UTF8
                Remove-Item $outputFile -Force -ErrorAction SilentlyContinue
                $result
            }
        }
    } -ArgumentList $Agent, $Prompt, $LogFile, $PWD

    $timedOut = $false
    $result = $null

    if ($ITERATION_TIMEOUT -gt 0) {
        $completed = $agentJob | Wait-Job -Timeout $ITERATION_TIMEOUT
        if ($null -eq $completed) {
            # Timed out
            $agentJob | Stop-Job -PassThru | Remove-Job -Force
            $timedOut = $true
            Write-Host ""
            Write-Host "TIMEOUT: Agent exceeded ${ITERATION_TIMEOUT}s limit"
            "timeout" | Add-Content -Path $LogFile
            Write-Status "timeout"
        } else {
            $result = $agentJob | Receive-Job
            $agentJob | Remove-Job -Force
        }
    } else {
        $result = $agentJob | Wait-Job | Receive-Job
        $agentJob | Remove-Job -Force
    }

    # Clean up heartbeat job
    $heartbeatJob | Stop-Job -PassThru | Remove-Job -Force -ErrorAction SilentlyContinue

    return @{
        Result   = ($result -join "`n")
        TimedOut = $timedOut
    }
}

function Send-Speech {
    param([string]$Text)
    try {
        Add-Type -AssemblyName System.Speech -ErrorAction SilentlyContinue
        $synth = New-Object System.Speech.Synthesis.SpeechSynthesizer
        $synth.Speak($Text)
        $synth.Dispose()
    } catch {
        # Speech not available, skip silently
    }
}

# ── Initialization ──────────────────────────────────────────────────────────

if (-not (Test-Path $LOG_DIR)) {
    New-Item -Path $LOG_DIR -ItemType Directory -Force | Out-Null
}

if (-not (Test-Path $TASKS_FILE)) {
    Write-Error "ERROR: $TASKS_FILE not found. Run the prd-to-tasks agent first."
    exit 1
}

if (-not (Test-Path $PROGRESS_FILE)) {
    @"
# Progress Log — T-RRX

Each iteration appends a structured entry.
The coding agent reads this file at the start of every session to get context.

---
"@ | Set-Content -Path $PROGRESS_FILE -Encoding UTF8
    Write-Host "Created $PROGRESS_FILE"
}

$agent = Resolve-Agent
if (-not $agent) {
    Write-Error "No supported agent found. Install 'claude' or 'codex', or set `$env:RALPH_AGENT."
    exit 1
}

Write-Host "Agent: $agent"
Write-Host "Tasks file: $TASKS_FILE"
Write-Host "Max iterations: $MAX_ITERATIONS"
Write-Host "Iteration timeout: ${ITERATION_TIMEOUT}s"
Write-Host ""
Write-Host "Monitor: Get-Content $STATUS_FILE"
Write-Host "Heartbeat: (Get-Item $HEARTBEAT_FILE).LastWriteTime"
Write-Host ""

# ── Prompt (identical to ralph.sh) ──────────────────────────────────────────

$AGENT_PROMPT = @'
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
'@

# ── Main Loop ───────────────────────────────────────────────────────────────

$script:iteration = 1

while ((Get-StatusCount "pending") -gt 0) {
    if ($script:iteration -gt $MAX_ITERATIONS) {
        Write-Host "Reached max iterations ($MAX_ITERATIONS). Stopping."
        break
    }

    $pending    = Get-StatusCount "pending"
    $inProgress = Get-StatusCount "in_progress"
    $doneCount  = Get-StatusCount "done"
    $timestamp  = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $logFile    = "$LOG_DIR/iteration-$($script:iteration)-$(Get-Date -Format 'yyyyMMdd-HHmmss').log"

    Write-Host ""
    Write-Host "================================================================="
    Write-Host "  Iteration $($script:iteration)  |  $timestamp"
    Write-Host "  pending: $pending  |  in_progress: $inProgress  |  done: $doneCount"
    Write-Host "================================================================="
    Write-Host ""

    # ── Run the agent ────────────────────────────────────────────────────

    $startTime = Get-Date
    $agentResult = Invoke-AgentWithTimeout -Agent $agent -Prompt $AGENT_PROMPT -LogFile $logFile
    $endTime = Get-Date
    $elapsed = ($endTime - $startTime)
    $elapsedMin = [math]::Floor($elapsed.TotalMinutes)
    $elapsedSec = $elapsed.Seconds

    Write-Host ""
    Write-Host "  Duration: ${elapsedMin}m ${elapsedSec}s"

    $result   = $agentResult.Result
    $timedOut = $agentResult.TimedOut

    # ── Process result ───────────────────────────────────────────────────

    if ($timedOut) {
        Write-Host "TIMEOUT: Iteration timed out after ${ITERATION_TIMEOUT}s. Moving on."
        Write-Status "timeout" "iteration=$($script:iteration) duration=$([math]::Round($elapsed.TotalSeconds))s"

    } elseif ($result -match '<promise>COMPLETE</promise>') {
        Write-Host "Task completed in iteration $($script:iteration)"
        Write-Status "completed" "iteration=$($script:iteration) duration=$([math]::Round($elapsed.TotalSeconds))s"

        $remaining = Get-StatusCount "pending"
        if ($remaining -eq 0) {
            Write-Host ""
            Write-Host "All tasks completed after $($script:iteration) iterations!"
            Write-Status "all_done"
            Send-Speech "All tasks are done!"
            exit 0
        }
        Write-Host "Remaining: $remaining pending tasks. Continuing..."
        Send-Speech "Task done. Continuing."

    } elseif ($result -match '<promise>IN_PROGRESS</promise>') {
        Write-Host "Task not finished - will retry next iteration"
        Write-Host "   Check $logFile for details"
        Write-Status "in_progress" "iteration=$($script:iteration) duration=$([math]::Round($elapsed.TotalSeconds))s"

    } else {
        Write-Host "WARNING: Agent exited without promise tag. Check $logFile"
        Write-Status "unknown_exit" "iteration=$($script:iteration) duration=$([math]::Round($elapsed.TotalSeconds))s"
    }

    $script:iteration++
}

$totalDone = Get-StatusCount "done"
Write-Host ""
Write-Host "Loop finished. Iterations: $($script:iteration - 1), Tasks done: $totalDone"
Write-Status "finished" "iterations=$($script:iteration - 1) tasks_done=$totalDone"
Send-Speech "Loop finished."
