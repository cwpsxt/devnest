import type {
  BatchResult, Finding, InstallReq, Installed, MirrorItem, Overview, PackageView, PortRow, Preset,
  ServiceRow, Settings, Task,
} from './types'

// Wails injects window.go (the bound Go methods) and window.runtime (events).
// Calling through this thin layer keeps the pages typed without depending on
// the generated wailsjs folder.
declare global {
  interface Window {
    go?: { main?: { App?: Record<string, (...args: unknown[]) => Promise<unknown>> } }
    runtime?: {
      EventsOn(name: string, cb: (data: any) => void): () => void
    }
  }
}

export const connected = () => !!window.go?.main?.App

function call<T>(method: string, ...args: unknown[]): Promise<T> {
  const fn = window.go?.main?.App?.[method]
  if (!fn) {
    return Promise.reject(new Error('界面没有连上后台程序（' + method + '）'))
  }
  return fn(...args) as Promise<T>
}

/** Text of whatever a failed call rejected with. Go errors arrive as strings. */
export function errText(e: unknown): string {
  if (typeof e === 'string') return e
  if (e instanceof Error) return e.message
  return String(e)
}

export function on<T = unknown>(event: 'task' | 'log' | 'changed', cb: (data: T) => void): () => void {
  return window.runtime ? window.runtime.EventsOn(event, cb) : () => {}
}

export const api = {
  overview: () => call<Overview>('Overview'),
  catalog: () => call<PackageView[]>('Catalog'),
  presets: () => call<Preset[]>('Presets'),
  installed: () => call<Installed[]>('Installed'),

  install: (req: InstallReq) => call<number[]>('Install', req),
  installMany: (reqs: InstallReq[]) => call<BatchResult>('InstallMany', reqs),
  uninstall: (id: string, version: string, dataMode: 'keep' | 'backup' | 'delete') =>
    call<number>('Uninstall', id, version, dataMode),
  activate: (id: string, version: string) => call<number>('Activate', id, version),
  importInstaller: (id: string, version: string) => call<string>('ImportInstaller', id, version),

  tasks: () => call<Task[]>('Tasks'),
  taskLog: (id: number) => call<string[]>('TaskLog', id),
  cancelTask: (id: number) => call<void>('CancelTask', id),
  clearTasks: () => call<void>('ClearTasks'),

  services: () => call<ServiceRow[]>('Services'),
  serviceAction: (name: string, action: 'start' | 'stop' | 'restart') => call<number>('ServiceAction', name, action),
  setAutoStart: (name: string, auto: boolean) => call<void>('SetAutoStart', name, auto),
  portInfo: (port: number) => call<PortRow>('PortInfo', port),
  killPid: (pid: number) => call<void>('KillPID', pid),
  readLog: (path: string) => call<string>('ReadLog', path),

  mirrors: () => call<MirrorItem[]>('Mirrors'),
  setMirror: (id: string, url: string) => call<void>('SetMirror', id, url),
  setAllMirrors: () => call<string[]>('SetAllMirrors'),

  scan: (ids: string[]) => call<Finding[]>('Scan', ids),
  clean: (ids: string[]) => call<number>('Clean', ids),

  settings: () => call<Settings>('Settings'),
  saveSettings: (s: Settings) => call<void>('SaveSettings', s),
  checkRoot: (root: string) => call<string>('CheckRoot', root),
  clearCache: () => call<void>('ClearCache'),

  pickFolder: (title: string) => call<string>('PickFolder', title),
  openPath: (path: string) => call<void>('OpenPath', path),
  openUrl: (url: string) => call<void>('OpenURL', url),
  copyText: (text: string) => call<void>('CopyText', text),
}
