<script>
  let { diff } = $props();

  // The statuses worth naming next to the path. A plain modification is what a
  // diff row already looks like, so it carries no tag.
  const TAGGED = ['added', 'removed', 'renamed', 'copied', 'changed'];

  const files = $derived(diff.files ?? []);

  // Which paths are expanded. A diff is read, not navigated, so several
  // patches may stay open at once.
  let open = $state({});

  function toggle(path) {
    open = { ...open, [path]: !open[path] };
  }

  // lineClass tints the three kinds of line a patch carries; context lines
  // stay plain. GitHub's per-file patch starts at a hunk header, so there are
  // no `+++`/`---` file headers to mistake for additions.
  function lineClass(line) {
    if (line.startsWith('@@')) return 'hunk';
    if (line.startsWith('+')) return 'add';
    if (line.startsWith('-')) return 'del';
    return 'ctx';
  }
</script>

<div class="diff">
  {#if files.length === 0}
    <p class="note">no files in this PR</p>
  {:else}
    <ul class="files">
      {#each files as file (file.path)}
        <li>
          <button
            type="button"
            class="row"
            aria-expanded={Boolean(open[file.path])}
            onclick={() => toggle(file.path)}
          >
            <span class="path">{open[file.path] ? '▾' : '▸'} {file.path}</span>
            {#if TAGGED.includes(file.status)}<span class="tag">{file.status}</span>{/if}
            <span class="counts">+{file.additions} −{file.deletions}</span>
          </button>
          {#if open[file.path]}
            {#if file.patchMissing}
              <p class="note">no patch (binary or too large)</p>
            {:else}
              <pre>{#each file.patch.split('\n') as line}<span class={lineClass(line)}
                  >{line}</span
                >{'\n'}{/each}</pre>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
    {#if diff.truncated}<p class="note">first 100 files shown</p>{/if}
  {/if}
</div>

<style>
  .diff {
    padding: 0 0.25rem 0.5rem;
  }
  .files {
    margin: 0;
    padding: 0;
  }
  .files li {
    list-style: none;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    min-height: 44px;
    padding: 0.25rem 0.5rem;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    font-size: 0.9em;
    text-align: left;
    cursor: pointer;
  }
  .path {
    overflow-wrap: anywhere;
  }
  .tag {
    flex: none;
    padding: 0 0.3rem;
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--muted);
    font-size: 0.8em;
  }
  .counts,
  .note {
    color: var(--muted);
    font-size: 0.85em;
  }
  .counts {
    flex: none;
    margin-left: auto;
  }
  .note {
    margin: 0;
    padding: 0 0.5rem 0.25rem;
  }
  /* The patch scrolls inside the card, so a long line never widens the page. */
  pre {
    margin: 0 0 0.5rem;
    padding: 0.4rem 0.5rem;
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--bg);
    font-size: 0.8em;
    line-height: 1.35;
  }
  .add {
    color: var(--ok);
  }
  .del {
    color: var(--error);
  }
  .hunk {
    color: var(--muted);
  }
</style>
