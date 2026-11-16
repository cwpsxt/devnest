import type { Finding, PackageView, ServiceRow, TaskState } from './types'

export function fmtBytes(n: number): string {
  if (!n || n < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return (v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)) + ' ' + units[i]
}

/** Go's zero time marshals as year 1; treat that as "no time". */
export function fmtTime(iso: string): string {
  if (!iso || iso.startsWith('0001-')) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (x: number) => String(x).padStart(2, '0')
  const sameDay = d.toDateString() === new Date().toDateString()
  const clock = `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  return sameDay ? clock : `${d.getMonth() + 1}月${d.getDate()}日 ${clock}`
}

export const CATEGORIES: { id: PackageView['category']; name: string }[] = [
  { id: 'lang', name: '语言与运行时' },
  { id: 'db', name: '数据库' },
  { id: 'dbtool', name: '数据库工具' },
  { id: 'mw', name: '中间件' },
  { id: 'ide', name: '开发工具' },
  { id: 'tool', name: '构建与其他' },
  { id: 'dep', name: '运行库' },
]

export const categoryName = (id: string) => CATEGORIES.find((c) => c.id === id)?.name ?? id

export const PRODUCT_NAME = '小熙 DevNest'

export const STUDIO = {
  name: '城关区智牍网络技术工作室（个体工商户）',
  icp: '陇ICP备2026010995号-1',
  icpUrl: 'https://beian.miit.gov.cn/',
}

export const FINDING_KINDS: Record<Finding['kind'], string> = {
  process: '进程',
  service: '服务',
  uninstall: '已装程序',
  data: '数据',
  dir: '目录',
  userdir: '用户目录',
  conf: '配置项',
  env: '环境变量',
  path: 'PATH',
  reg: '注册表',
}

export const TASK_STATES: Record<TaskState, string> = {
  queued: '排队中',
  running: '进行中',
  done: '完成',
  failed: '失败',
  canceled: '已取消',
}

export const SERVICE_STATES: Record<ServiceRow['state'], string> = {
  running: '运行中',
  stopped: '已停止',
  starting: '正在启动',
  stopping: '正在停止',
  missing: '服务不存在',
  unknown: '未知',
}

/** Two letters shown in the coloured tile of a package. */
export function initials(name: string): string {
  const ascii = name.replace(/[^A-Za-z0-9+#.]/g, '')
  if (ascii.length >= 2) return ascii.slice(0, 2)
  return name.slice(0, 2)
}

const TILE_COLORS = ['#3b6cf6', '#0ea5a4', '#f59e0b', '#ef4444', '#8b5cf6', '#10b981', '#ec4899', '#0284c7', '#d97706', '#4f46e5']

/** A stable colour per package id. */
export function tileColor(id: string): string {
  let h = 0
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return TILE_COLORS[h % TILE_COLORS.length]
}

export function randomPassword(len = 14): string {
  // Only characters every recipe accepts (letters, digits, _ @ # . + -).
  const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'
  const buf = new Uint32Array(len)
  crypto.getRandomValues(buf)
  let out = ''
  for (const n of buf) out += alphabet[n % alphabet.length]
  return out
}
