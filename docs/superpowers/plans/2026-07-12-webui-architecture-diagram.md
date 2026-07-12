# WebUI Architecture Diagram Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a static "Architecture" section to the Bridge WebUI (`web/src/App.svelte`) with an inline SVG diagram showing how `ai-instructions`, `bridge`, and `agent-pipeline` relate (convention sync + issue-label-to-PR flow).

**Architecture:** A hand-authored SVG file (`web/src/lib/architecture.svg`) is read at build time and inlined directly into `App.svelte`'s markup via Vite's `?raw` import (so it's real inline `<svg>` markup, not an `<img src>`, and inherits page text color via `currentColor`). A new `<section>` with an `<h2>Architecture</h2>` heading is added below the existing "Agents" section.

**Tech Stack:** Svelte 5, Vite, Vitest + `@testing-library/svelte` (already a devDependency, not yet used elsewhere in this project — this plan is the first consumer).

## Global Constraints

- No Go changes: `internal/web`, `internal/api`, and the REST/SSE surface are untouched.
- No new npm dependency.
- SVG uses `currentColor` / inherited text color only — no hardcoded colors or fonts (the app has no theme/CSS system yet).
- Out of scope: any WebUI theming/dark-mode system, the interactive per-repo architecture visualizer (roadmap #4 / `bridge-poc2.html`), wiring the diagram to live/dynamic data.

---

### Task 1: Architecture SVG asset

**Files:**
- Create: `web/src/lib/architecture.svg`

**Interfaces:**
- Produces: a static SVG file with a top-level `<svg>` element, imported as a raw string by Task 2 via `import architectureSvg from './lib/architecture.svg?raw'`.

- [ ] **Step 1: Write the SVG file**

Create `web/src/lib/architecture.svg`:

```svg
<svg viewBox="0 0 720 220" xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="arch-title arch-desc" fill="none" stroke="currentColor" stroke-width="1.5">
  <title id="arch-title">Repo architecture</title>
  <desc id="arch-desc">ai-instructions syncs conventions into bridge and agent-pipeline. bridge labels an issue ai-implement for agent-pipeline, which opens a draft PR.</desc>

  <defs>
    <marker id="arch-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M0,0 L10,5 L0,10 z" fill="currentColor" stroke="none" />
    </marker>
  </defs>

  <!-- ai-instructions box -->
  <rect x="20" y="20" width="160" height="50" rx="6" />
  <text x="100" y="50" text-anchor="middle" stroke="none" fill="currentColor" font-size="14">ai-instructions</text>

  <!-- bridge box -->
  <rect x="20" y="150" width="160" height="50" rx="6" />
  <text x="100" y="180" text-anchor="middle" stroke="none" fill="currentColor" font-size="14">bridge</text>

  <!-- agent-pipeline box -->
  <rect x="380" y="85" width="160" height="50" rx="6" />
  <text x="460" y="115" text-anchor="middle" stroke="none" fill="currentColor" font-size="14">agent-pipeline</text>

  <!-- GitHub/Forgejo box -->
  <rect x="600" y="85" width="100" height="50" rx="6" />
  <text x="650" y="110" text-anchor="middle" stroke="none" fill="currentColor" font-size="12">GitHub /</text>
  <text x="650" y="124" text-anchor="middle" stroke="none" fill="currentColor" font-size="12">Forgejo</text>

  <!-- ai-instructions -> bridge -->
  <path d="M100,70 L100,150" marker-end="url(#arch-arrow)" />
  <text x="108" y="112" font-size="11" stroke="none" fill="currentColor">sync via /sync-ai-instructions</text>

  <!-- ai-instructions -> agent-pipeline -->
  <path d="M180,45 L380,100" marker-end="url(#arch-arrow)" />
  <text x="200" y="65" font-size="11" stroke="none" fill="currentColor">sync via /sync-ai-instructions</text>

  <!-- bridge -> agent-pipeline -->
  <path d="M180,165 L380,125" marker-end="url(#arch-arrow)" />
  <text x="200" y="200" font-size="11" stroke="none" fill="currentColor">issue labeled ai-implement</text>

  <!-- agent-pipeline -> GitHub/Forgejo -->
  <path d="M540,110 L600,110" marker-end="url(#arch-arrow)" />
  <text x="545" y="150" font-size="11" stroke="none" fill="currentColor">opens draft PR</text>
</svg>
```

- [ ] **Step 2: Verify the file is well-formed XML**

Run: `node -e "require('fs').readFileSync('web/src/lib/architecture.svg','utf8').match(/<svg[\s\S]*<\/svg>/) ? console.log('ok') : process.exit(1)"`
Expected: prints `ok`

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/architecture.svg
git commit -m "feat(web): add architecture diagram SVG asset"
```

---

### Task 2: Architecture section in App.svelte

**Files:**
- Modify: `web/src/App.svelte`
- Test: `web/src/App.test.js`

**Interfaces:**
- Consumes: `web/src/lib/architecture.svg` (raw string import from Task 1) via `import architectureSvg from './lib/architecture.svg?raw'`.
- Produces: a `<section>` in `App.svelte`'s rendered output containing an `<h2>` with text `Architecture` followed by the inlined `<svg>` markup — this is the full surface other tasks/tests rely on; there are no further tasks in this plan.

- [ ] **Step 1: Write the failing test**

Create `web/src/App.test.js`:

```js
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/svelte'

vi.mock('./lib/stores/repos.js', () => ({
  loadRepos: vi.fn(),
  repos: { subscribe: (fn) => { fn([]); return () => {} } },
}))
vi.mock('./lib/stores/agents.js', () => ({
  loadAgents: vi.fn(),
  agents: { subscribe: (fn) => { fn([]); return () => {} } },
}))

describe('App', () => {
  beforeEach(() => vi.clearAllMocks())

  it('renders an Architecture section with the inlined SVG diagram', async () => {
    const { default: App } = await import('./App.svelte')
    render(App)

    const heading = screen.getByRole('heading', { name: 'Architecture' })
    expect(heading).toBeInTheDocument()

    const section = heading.closest('section')
    expect(section.querySelector('svg')).not.toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run App.test.js`
Expected: FAIL — no element with role "heading" and name "Architecture" found

- [ ] **Step 3: Add the Architecture section to App.svelte**

Edit `web/src/App.svelte` — add the import at the top of the `<script>` block (after the existing imports):

```js
  import architectureSvg from './lib/architecture.svg?raw';
```

Add a new `<section>` after the closing `</section>` of the Agents section (before `</main>`):

```svelte
  <section>
    <h2>Architecture</h2>
    <p>How ai-instructions, bridge, and agent-pipeline fit together:</p>
    {@html architectureSvg}
  </section>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run App.test.js`
Expected: PASS

- [ ] **Step 5: Run the full web test suite**

Run: `cd web && npx vitest run`
Expected: all tests pass (including the pre-existing `sse.test.js`)

- [ ] **Step 6: Manual visual verification**

Run: `cd web && npm run dev`, open the printed local URL in a browser.
Expected: an "Architecture" section renders below "Agents" showing the four-box diagram (ai-instructions, bridge, agent-pipeline, GitHub/Forgejo) with labeled arrows, using the page's default text color; the existing Agents section layout is unchanged.

- [ ] **Step 7: Commit**

```bash
git add web/src/App.svelte web/src/App.test.js
git commit -m "feat(web): add Architecture section with repo-relationship diagram"
```
