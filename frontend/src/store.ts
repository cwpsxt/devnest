import { computed, reactive } from 'vue'
import { api, connected, errText, on } from './api'
import type { Overview, PackageView, Preset, ServiceRow, Task } from './types'

export type Page = 'home' | 'software' | 'services' | 'versions' | 'mirrors' | 'clean' | 'tasks' | 'settings'

export interface Toast {
  id: number
  kind: 'ok' | 'error' | 'info'
  text: string
}

export interface ConfirmRequest {
  title: string
  message: string
  details?: string[]
  okText?: string
  danger?: boolean
  resolve: (ok: boolean) => void
}

export const state = reactive({
  ready: false,
  fatal: '',
  page: 'home' as Page,
  overview: null as Overview | null,
  catalog: [] as PackageView[],
  presets: [] as Preset[],
  services: [] as ServiceRow[],
  tasks: [] as Task[],
  toasts: [] as Toast[],
  confirm: null as ConfirmRequest | null,
  /** id of the task whose log is open, 0 for none */
  logTask: 0,
  logs: {} as Record<number, string[]>,
  /** package the software page should scroll to and open */
  focusPkg: '',
})

export const activeTasks = computed(() => state.tasks.filter((t) => t.state === 'running' || t.state === 'queued'))
export const runningTask = computed(() => state.tasks.find((t) => t.state === 'running'))
export const busy = computed(() => activeTasks.value.length > 0)

/** True while a task for this package is queued or running. */
export function pkgBusy(id: string): Task | undefined {
  return activeTasks.value.find((t) => t.pkgId === id || t.pkgId?.startsWith(id + '@'))
}

let toastSeq = 0

export function toast(kind: Toast['kind'], text: string) {
  const id = ++toastSeq
  state.toasts.push({ id, kind, text })
  setTimeout(() => dismissToast(id), kind === 'error' ? 9000 : 4000)
}

export function dismissToast(id: number) {
  const i = state.toasts.findIndex((t) => t.id === id)
  if (i >= 0) state.toasts.splice(i, 1)
}

/** Shows the failure of a backend call as a toast. */
export function fail(e: unknown, what = '') {
  toast('error', (what ? what + '：' : '') + errText(e))
}

export function confirmDialog(req: Omit<ConfirmRequest, 'resolve'>): Promise<boolean> {
  return new Promise((resolve) => {
    state.confirm = { ...req, resolve }
  })
}

export function answerConfirm(ok: boolean) {
  const c = state.confirm
  state.confirm = null
  c?.resolve(ok)
}

export function go(page: Page, focusPkg = '') {
  state.page = page
  state.focusPkg = focusPkg
}

export async function refreshOverview() {
  state.overview = await api.overview()
}

export async function refreshCatalog() {
  state.catalog = await api.catalog()
}

export async function refreshServices() {
  state.services = await api.services()
}

export async function refreshTasks() {
  state.tasks = await api.tasks()
}

export async function refreshAll() {
  await Promise.all([refreshOverview(), refreshCatalog(), refreshServices(), refreshTasks()])
}

let refreshTimer = 0

/** Several "changed" events usually arrive together; refresh once. */
function refreshSoon() {
  clearTimeout(refreshTimer)
  refreshTimer = window.setTimeout(() => {
    Promise.all([refreshOverview(), refreshCatalog(), refreshServices()]).catch(() => {})
  }, 150)
}

function onTask(t: Task) {
  const i = state.tasks.findIndex((x) => x.id === t.id)
  const before = i >= 0 ? state.tasks[i].state : undefined
  if (i >= 0) state.tasks[i] = t
  else state.tasks.push(t)
  if (before === t.state) return
  if (t.state === 'done') {
    toast('ok', t.title + ' 完成')
    refreshSoon()
  } else if (t.state === 'failed') {
    toast('error', t.title + ' 失败：' + (t.error || '详见日志'))
    refreshSoon()
  } else if (t.state === 'canceled') {
    refreshSoon()
  }
}

const MAX_LOG_LINES = 3000

function onLog(e: { id: number; line: string }) {
  const lines = state.logs[e.id]
  if (!lines) return // nobody opened this log yet; it is fetched in full when opened
  lines.push(e.line)
  if (lines.length > MAX_LOG_LINES) lines.splice(0, lines.length - MAX_LOG_LINES)
}

export async function openLog(taskId: number) {
  state.logTask = taskId
  try {
    state.logs[taskId] = await api.taskLog(taskId)
  } catch (e) {
    fail(e, '读取日志')
  }
}

export function closeLog() {
  state.logTask = 0
}

/** Opens the log of the first task and tells the user the work was queued. */
export function queued(taskIds: number[] | number, text: string) {
  const ids = Array.isArray(taskIds) ? taskIds : [taskIds]
  toast('info', text)
  refreshTasks().catch(() => {})
  return ids
}

export async function start() {
  // The Wails bridge is injected a moment after the page loads.
  for (let i = 0; i < 50 && !connected(); i++) {
    await new Promise((r) => setTimeout(r, 100))
  }
  if (!connected()) {
    state.fatal = '界面没有连上后台程序。请直接运行 DevNest.exe，而不是在浏览器里打开这个页面。'
    return
  }
  on<Task>('task', onTask)
  on<{ id: number; line: string }>('log', onLog)
  on('changed', refreshSoon)
  try {
    await refreshAll()
    state.presets = await api.presets()
    state.ready = true
  } catch (e) {
    state.fatal = '读取数据失败：' + errText(e)
  }
}
