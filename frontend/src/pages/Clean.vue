<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Icon from '../components/Icon.vue'
import { api, errText } from '../api'
import { confirmDialog, fail, openLog, refreshTasks, state } from '../store'
import type { Finding } from '../types'
import { FINDING_KINDS, fmtBytes } from '../util'

const findings = ref<Finding[] | null>(null)
const scanning = ref(false)
const scanError = ref('')
const checked = reactive<Record<string, boolean>>({})
/** id of the cleanup task that is running, 0 when none */
const cleanTask = ref(0)
const lastResult = ref<'' | 'done' | 'failed' | 'canceled'>('')

const groups = computed(() => {
  const map = new Map<string, { id: string; name: string; items: Finding[] }>()
  for (const f of findings.value ?? []) {
    if (!map.has(f.pkgId)) map.set(f.pkgId, { id: f.pkgId, name: f.pkgName, items: [] })
    map.get(f.pkgId)!.items.push(f)
  }
  return [...map.values()]
})

const selectable = (f: Finding) => !f.manual
const selected = computed(() => (findings.value ?? []).filter((f) => selectable(f) && checked[f.id]))
const cleaning = computed(() => cleanTask.value !== 0)

async function scan() {
  scanning.value = true
  scanError.value = ''
  try {
    const list = await api.scan([])
    for (const k of Object.keys(checked)) delete checked[k]
    for (const f of list) checked[f.id] = f.checked && selectable(f)
    findings.value = list
  } catch (e) {
    scanError.value = errText(e)
  } finally {
    scanning.value = false
  }
}

function groupState(items: Finding[]): 'all' | 'some' | 'none' {
  const usable = items.filter(selectable)
  const n = usable.filter((f) => checked[f.id]).length
  return n === 0 ? 'none' : n === usable.length ? 'all' : 'some'
}

function toggleGroup(items: Finding[]) {
  const on = groupState(items) !== 'all'
  for (const f of items) if (selectable(f)) checked[f.id] = on
}

function setAll(on: boolean) {
  for (const f of findings.value ?? []) if (selectable(f)) checked[f.id] = on && f.checked
}

async function clean() {
  const items = selected.value
  if (!items.length) return
  const count = (k: Finding['kind']) => items.filter((f) => f.kind === k).length
  const parts: string[] = []
  if (count('process')) parts.push(`结束 ${count('process')} 个正在运行的进程`)
  if (count('service')) parts.push(`停止并删除 ${count('service')} 个服务`)
  if (count('uninstall')) parts.push(`运行 ${count('uninstall')} 个程序自带的卸载`)
  if (count('data')) parts.push(`把 ${count('data')} 个数据目录移到备份目录（不删除）`)
  if (count('dir') + count('userdir')) parts.push(`删除 ${count('dir') + count('userdir')} 个目录`)
  if (count('conf')) parts.push(`从配置文件里删掉 ${count('conf')} 个设置（先备份到文本文件）`)
  if (count('env')) parts.push(`删除 ${count('env')} 个环境变量（先备份到文本文件）`)
  if (count('path')) parts.push(`从 PATH 移除 ${count('path')} 个条目（先备份）`)
  if (count('reg')) parts.push(`删除 ${count('reg')} 个注册表项`)

  const ok = await confirmDialog({
    title: `清理选中的 ${items.length} 项`,
    message: '将要做下面这些事。删除的安装目录和注册表项不能恢复，请确认列表里没有你还要用的东西。',
    details: parts,
    okText: '开始清理',
    danger: true,
  })
  if (!ok) return
  try {
    lastResult.value = ''
    cleanTask.value = await api.clean(items.map((f) => f.id))
    await refreshTasks()
    openLog(cleanTask.value)
  } catch (e) {
    fail(e, '清理')
  }
}

// When the cleanup task ends, scan again so the list shows what is left.
watch(
  () => state.tasks.find((t) => t.id === cleanTask.value)?.state,
  (s) => {
    if (s === 'done' || s === 'failed' || s === 'canceled') {
      lastResult.value = s
      cleanTask.value = 0
      scan()
    }
  },
)
</script>

<template>
  <div class="page-head">
    <div>
      <h1>环境清理</h1>
      <div class="sub">找出以前装过的 MySQL、JDK、Node.js 等留下的东西。先列出来，你勾选确认后才会动手。用本工具装的软件不在此列，请到软件库里卸载。</div>
    </div>
    <span class="spacer" />
    <button class="btn" :class="{ primary: findings === null }" :disabled="scanning || cleaning" @click="scan">
      <span v-if="scanning" class="spin" /><Icon v-else name="search" />{{ findings === null ? '开始扫描' : '重新扫描' }}
    </button>
  </div>

  <div v-if="scanError" class="note danger selectable" style="margin-bottom: 14px">扫描失败：{{ scanError }}</div>
  <div v-if="lastResult === 'done'" class="note" style="margin-bottom: 14px; border-color: #bfe5cf; background: var(--ok-soft); color: #0b6b3c">
    清理完成。下面是重新扫描的结果；被移走的数据在安装目录的 backup 文件夹里。新开的命令行窗口才会看到改过的环境变量。
  </div>
  <div v-if="lastResult === 'failed'" class="note warn" style="margin-bottom: 14px">有几项没有清理成功，原因在「任务与日志」里。下面是重新扫描后还剩的项目。</div>

  <div v-if="findings === null && !scanning" class="card empty">
    <div class="big">还没有扫描</div>
    <div>扫描只是查看，不会改动任何东西。会检查：Windows 服务、「应用和功能」里的程序、常见安装目录、环境变量、PATH 和注册表。</div>
  </div>

  <div v-else-if="findings && !findings.length" class="card empty">
    <div class="big">很干净，没有发现旧的安装痕迹</div>
    <div>可以直接去安装了。</div>
  </div>

  <template v-else-if="findings">
    <div class="row" style="margin-bottom: 10px">
      <span class="muted">发现 {{ findings.length }} 项，已选 {{ selected.length }} 项</span>
      <a @click="setAll(true)">恢复默认勾选</a>
      <a @click="setAll(false)">全不选</a>
      <span class="spacer" />
      <button class="btn danger solid lg" :disabled="!selected.length || cleaning || scanning" @click="clean">
        <span v-if="cleaning" class="spin" /><Icon v-else name="trash" />{{ cleaning ? '正在清理…' : `清理选中的 ${selected.length} 项` }}
      </button>
    </div>

    <div v-for="g in groups" :key="g.id" class="card" style="padding: 0">
      <label class="row" style="padding: 12px 16px; border-bottom: 1px solid var(--line); cursor: pointer">
        <input
          type="checkbox"
          class="check"
          :checked="groupState(g.items) === 'all'"
          :indeterminate.prop="groupState(g.items) === 'some'"
          :disabled="!g.items.some(selectable)"
          @change="toggleGroup(g.items)"
        />
        <h2>{{ g.name }}</h2>
        <span class="tag">{{ g.items.length }} 项</span>
      </label>
      <label v-for="f in g.items" :key="f.id" class="row" style="padding: 9px 16px; border-bottom: 1px solid var(--line); align-items: flex-start" :style="{ cursor: selectable(f) ? 'pointer' : 'default' }">
        <input v-model="checked[f.id]" type="checkbox" class="check" style="margin-top: 3px" :disabled="!selectable(f)" />
        <span class="tag" :class="{ warn: f.kind === 'data', danger: f.kind === 'process' }" style="width: 72px; justify-content: center; flex: none">{{ FINDING_KINDS[f.kind] ?? f.kind }}</span>
        <div style="flex: 1; min-width: 0">
          <div>{{ f.title }}</div>
          <div v-if="f.detail" class="mono faint selectable" style="word-break: break-all">{{ f.detail }}</div>
          <div v-if="f.note" class="small" style="color: var(--warn); margin-top: 2px"><Icon name="alert" :size="12" style="vertical-align: -1px" /> {{ f.note }}</div>
        </div>
        <span v-if="f.size > 0" class="muted small" style="flex: none">{{ fmtBytes(f.size) }}</span>
        <span v-else-if="f.size < 0" class="faint small" style="flex: none" title="目录太大，没有逐个统计">很大</span>
      </label>
    </div>
  </template>
</template>
