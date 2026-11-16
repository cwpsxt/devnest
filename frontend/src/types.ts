// Shapes of the data the Go side sends. They mirror the JSON tags in
// internal/core (view.go, types.go, scan.go, services.go, mirrors.go).

export interface ReleaseView {
  version: string
  label: string
  size: number
  cached: boolean
  installed: boolean
  active: boolean
  source: string
}

export interface Choice {
  value: string
  label: string
}

export interface Option {
  key: string
  label: string
  type: 'text' | 'password' | 'port' | 'bool' | 'select'
  default: string
  help?: string
  choices?: Choice[]
  minLen?: number
  pattern?: string
  portName?: string
}

export interface KV {
  label: string
  value: string
}

export interface PortRef {
  name: string
  port: number
  service?: string
}

export interface Installed {
  id: string
  version: string
  dir: string
  dataDir?: string
  options?: Record<string, string>
  services?: string[]
  ports?: PortRef[]
  env?: string[]
  paths?: string[]
  info?: KV[]
  logs?: string[]
  active: boolean
  at: string
}

export interface PackageView {
  id: string
  name: string
  category: 'lang' | 'db' | 'dbtool' | 'mw' | 'ide' | 'tool' | 'dep'
  desc: string
  homepage: string
  notes: string[]
  releases: ReleaseView[]
  options: Option[]
  requires: string[]
  multi: boolean
  homeVar: string
  hasData: boolean
  installed: Installed[]
}

export interface DriveInfo {
  root: string
  freeBytes: number
  totalBytes: number
}

export interface Overview {
  version: string
  os: string
  admin: boolean
  simulated: boolean
  root: string
  rootError: string
  drives: DriveInfo[]
  installed: number
  services: number
  running: number
  cacheSize: number
  logDir: string
}

export interface Preset {
  id: string
  name: string
  desc: string
  items: string[]
}

export interface InstallReq {
  id: string
  version: string
  options: Record<string, string>
}

export interface BatchResult {
  tasks: number[]
  skipped: string[]
  errors: string[]
}

export type TaskState = 'queued' | 'running' | 'done' | 'failed' | 'canceled'

export interface Task {
  id: number
  kind: string
  title: string
  pkgId?: string
  state: TaskState
  progress: number
  message: string
  error?: string
  started: string
  ended: string
}

export interface PortRow {
  name: string
  port: number
  listening: boolean
  owner: string
  ownerPid: number
}

export interface ServiceRow {
  name: string
  pkgId: string
  pkgName: string
  version: string
  state: 'running' | 'stopped' | 'starting' | 'stopping' | 'missing' | 'unknown'
  startType: string
  pid: number
  ports: PortRow[]
  logs: string[]
  dir: string
}

export interface MirrorItem {
  id: string
  name: string
  desc: string
  file: string
  choices: Choice[]
  current: string
  custom: boolean
}

export interface Finding {
  id: string
  pkgId: string
  pkgName: string
  kind: 'process' | 'service' | 'uninstall' | 'dir' | 'data' | 'userdir' | 'conf' | 'env' | 'path' | 'reg'
  title: string
  detail: string
  checked: boolean
  size: number
  note?: string
  /** cannot be removed automatically; shown for information only */
  manual: boolean
}

export interface Settings {
  root: string
  githubProxy: string
  desktopShortcuts: boolean
}
