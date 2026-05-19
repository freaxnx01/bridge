# Ralph journal

iter 1 (2026-05-19): Phase 1 (Planning) — wrote PLAN.md from 7 open issues. Dep markers found: #3→#4, #5→#3,#4. No cycles. Effort scores (lower=easier=first):

- #6=0  (no flag/config keywords, short body)
- #7=+2 (flag keywords)
- #4=+4 (flag+length)
- #3=+4 (flag+length, layer 1 after #4)
- #5=+4 (flag+length, layer 2 after #3)
- #8=+8 (epic label +4, flag, cross-platform)
- #9=+9 (enhancement+long body +3, flag, length, xplat keywords)

Surprise: #9 outscored #8 by 1 point because #9's body is long and hits cross-platform keywords (WSL2 / execution-context language). Operator intuition was that #8 (Windows/PowerShell port) is the heaviest piece. If you agree, manually swap #8 and #9 in PLAN.md before Phase 2 picks #8. The rubric is a heuristic; manual override is allowed.

iter 1: also added `.claude/` to `.gitignore` (the ralph-loop plugin writes session state there; should not be tracked).
