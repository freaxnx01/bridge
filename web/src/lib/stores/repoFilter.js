import { writable } from 'svelte/store'

const STORAGE_KEY = 'bridge.repoFilter'

// A repo key is `forge:owner/name`, so two same-named repos on different
// forges stay distinct.
const empty = () => ({ forge: '', owner: '', repos: [] })

export const repoFilter = writable(empty())

// ownerOf is an item's owner: its repo up to the last slash. ADO projects
// contain spaces and parentheses, so nothing else may be assumed about it.
export function ownerOf(repo) {
  const cut = (repo ?? '').lastIndexOf('/')
  return cut < 0 ? '' : repo.slice(0, cut)
}

// repoKey keys an item (`{forge, repo}`) and an /api/repos entry
// (`{forge, owner, name}`) the same way.
export function repoKey(item) {
  return `${item.forge}:${item.repo ?? `${item.owner}/${item.name}`}`
}

export function isFiltered(filter) {
  return Boolean(filter.forge || filter.owner || filter.repos.length > 0)
}

// matches reports whether an item passes the filter. An item outside every
// repo (`repo: ''`, e.g. a session in no worktree) belongs to no forge or
// owner, so any filter hides it.
export function matches(filter, item) {
  if (!isFiltered(filter)) return true
  if (!item.repo) return false
  if (filter.forge && item.forge !== filter.forge) return false
  if (filter.owner && ownerOf(item.repo) !== filter.owner) return false
  if (filter.repos.length === 0) return true
  return filter.repos.includes(repoKey(item))
}

// setForge picks a forge (`''` = all), resets the owner and drops selected
// repos of other forges.
export function setForge(forge) {
  repoFilter.update((filter) => ({
    forge,
    owner: '',
    repos: forge ? filter.repos.filter((key) => key.startsWith(`${forge}:`)) : filter.repos,
  }))
}

// setOwner picks an owner of the current forge (`''` = all) and drops
// selected repos of other owners.
export function setOwner(owner) {
  repoFilter.update((filter) => ({
    ...filter,
    owner,
    repos: owner ? filter.repos.filter((key) => key.startsWith(`${filter.forge}:${owner}/`)) : filter.repos,
  }))
}

export function toggleRepo(key) {
  repoFilter.update((filter) => ({
    ...filter,
    repos: filter.repos.includes(key) ? filter.repos.filter((k) => k !== key) : [...filter.repos, key],
  }))
}

export function setRepos(keys) {
  repoFilter.update((filter) => ({ ...filter, repos: [...keys] }))
}

export function clearFilter() {
  repoFilter.set(empty())
}

export function parseQuery(search) {
  const params = new URLSearchParams(search)
  return {
    forge: params.get('forge') ?? '',
    owner: params.get('owner') ?? '',
    repos: params.getAll('repo'),
  }
}

// toQuery rewrites the filter params of `search` and keeps every other one.
export function toQuery(filter, search) {
  const params = new URLSearchParams(search)
  params.delete('forge')
  params.delete('owner')
  params.delete('repo')
  if (filter.forge) params.set('forge', filter.forge)
  if (filter.owner) params.set('owner', filter.owner)
  for (const key of filter.repos) params.append('repo', key)
  const query = params.toString()
  return query ? `?${query}` : ''
}

// initRepoFilter seeds the store — the URL wins over the stored filter — and
// then keeps both in step with every change. Returns the unsubscribe function.
export function initRepoFilter(win = window) {
  const fromUrl = parseQuery(win.location.search)
  repoFilter.set(fromUrl.forge || fromUrl.repos.length > 0 ? fromUrl : readStored(win))
  return repoFilter.subscribe((filter) => {
    writeStored(win, filter)
    win.history.replaceState(null, '', win.location.pathname + toQuery(filter, win.location.search) + win.location.hash)
  })
}

// readStored never fails the page: a browser that denies storage (private
// mode) or junk under the key both read as "nothing stored".
function readStored(win) {
  try {
    const raw = win.localStorage.getItem(STORAGE_KEY)
    if (!raw) return empty()
    const parsed = JSON.parse(raw)
    return {
      forge: typeof parsed.forge === 'string' ? parsed.forge : '',
      owner: typeof parsed.owner === 'string' ? parsed.owner : '',
      repos: Array.isArray(parsed.repos) ? parsed.repos.filter((key) => typeof key === 'string') : [],
    }
  } catch {
    return empty()
  }
}

function writeStored(win, filter) {
  try {
    win.localStorage.setItem(STORAGE_KEY, JSON.stringify(filter))
  } catch {
    // Storage denied: the filter still applies for this page view.
  }
}
