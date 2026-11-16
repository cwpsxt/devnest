<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import Icon from '../components/Icon.vue'
import { api, errText } from '../api'
import { busy, confirmDialog, fail, refreshCatalog, refreshOverview, state, toast } from '../store'
import type { Settings } from '../types'
import { fmtBytes } from '../util'

const saved = ref<Settings | null>(null)
const form = reactive<Settings>({ root: '', githubProxy: '', desktopShortcuts: true })
const rootProblem = ref('')
const saving = ref(false)
const clearing = ref(false)

const hasInstalled = computed(() => state.catalog.some((p) => p.installed.length))
const proxyProblem = computed(() => {
  const v = form.githubProxy.trim()
  return v && !v.startsWith('https://') ? '地址必须以 https:// 开头' : ''
})
const dirty = computed(
  () =>
    !!saved.value &&
    (form.root.trim() !== saved.value.root || form.githubProxy.trim() !== saved.value.githubProxy || form.desktopShortcuts !== saved.value.desktopShortcuts),
)

async function load() {
  try {
    const s = await api.settings()
    saved.value = s
    Object.assign(form, s)
  } catch (e) {
    fail(e, '读取设置')
  }
}

onMounted(load)

let checkTimer = 0
watch(
  () => form.root,
  (v) => {
    clearTimeout(checkTimer)
    checkTimer = window.setTimeout(async () => {
      try {
        rootProblem.value = await api.checkRoot(v)
      } catch (e) {
        rootProblem.value = errText(e)
      }
    }, 200)
  },
)

async function pick() {
  try {
    const dir = await api.pickFolder('选择安装目录')
    if (dir) form.root = dir
  } catch (e) {
    fail(e)
  }
}

async function save() {
  saving.value = true
  try {
    await api.saveSettings({ root: form.root.trim(), githubProxy: form.githubProxy.trim(), desktopShortcuts: form.desktopShortcuts })
    toast('ok', '设置已保存')
    await Promise.all([load(), refreshOverview(), refreshCatalog()])
  } catch (e) {
    fail(e, '保存设置')
  } finally {
    saving.value = false
  }
}

async function clearCache() {
  const ok = await confirmDialog({
    title: '清空安装包缓存',
    message: `会删除已下载的安装包（${fmtBytes(state.overview?.cacheSize ?? 0)}）。已经装好的软件不受影响，以后再装同一个版本需要重新下载。`,
    okText: '清空',
    danger: true,
  })
  if (!ok) return
  clearing.value = true
  try {
    await api.clearCache()
    toast('ok', '缓存已清空')
    await Promise.all([refreshOverview(), refreshCatalog()])
  } catch (e) {
    fail(e, '清空缓存')
  } finally {
    clearing.value = false
  }
}

async function open(path: string) {
  try {
    await api.openPath(path)
  } catch (e) {
    fail(e)
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>设置</h1>
    </div>
    <span class="spacer" />
    <button class="btn primary" :disabled="!dirty || saving || !!rootProblem || !!proxyProblem" @click="save">保存设置</button>
  </div>

  <div class="card">
    <div class="field">
      <label for="set-root">安装目录</label>
      <div class="row">
        <input id="set-root" v-model="form.root" class="input mono" :class="{ invalid: rootProblem }" style="flex: 1" spellcheck="false" :disabled="hasInstalled" />
        <button class="btn" :disabled="hasInstalled" @click="pick"><Icon name="folder" />选择文件夹</button>
        <button class="btn ghost" title="在文件管理器里打开" @click="open(saved?.root ?? '')"><Icon name="external" /></button>
      </div>
      <div v-if="rootProblem" class="error">{{ rootProblem }}</div>
      <div class="help">
        所有软件装在这个目录的 apps 下，数据在 data，安装包在 cache，清理和卸载时的备份在 backup。路径不能有空格和中文。
        <template v-if="hasInstalled">已经有软件装在这里，要换目录请先把它们卸载。</template>
      </div>
    </div>

    <div class="field">
      <label for="set-proxy">GitHub 加速前缀（可选）</label>
      <input id="set-proxy" v-model="form.githubProxy" class="input mono" :class="{ invalid: proxyProblem }" style="width: 100%" placeholder="留空表示直接连 GitHub" spellcheck="false" />
      <div v-if="proxyProblem" class="error">{{ proxyProblem }}</div>
      <div class="help">
        Redis、RabbitMQ、Erlang、MinGW 等只能从 GitHub 下载。连不上时，可以填一个你信得过的加速地址，下载时会先试「前缀 + 原地址」，不行再直连。
        加速站是第三方，文件会经过它：有校验值的安装包会被核对，没有校验值的只能核对大小。
      </div>
    </div>

    <label class="row" style="cursor: pointer">
      <input v-model="form.desktopShortcuts" type="checkbox" class="check" />
      安装 IDEA、VS Code 这类工具时在桌面放快捷方式
    </label>
  </div>

  <div class="card">
    <div class="row">
      <div style="flex: 1">
        <h2>安装包缓存</h2>
        <div class="muted small">下载过的安装包留在 cache 目录，重装时不用再下。现在占用 {{ fmtBytes(state.overview?.cacheSize ?? 0) }}。</div>
      </div>
      <button class="btn danger" :disabled="clearing || busy || !state.overview?.cacheSize" :title="busy ? '有任务在进行，稍后再清' : ''" @click="clearCache">清空缓存</button>
    </div>
  </div>

  <div class="card">
    <h2 style="margin-bottom: 10px">关于</h2>
    <dl class="kv selectable">
      <dt>版本</dt>
      <dd>DevNest {{ state.overview?.version }}</dd>
      <dt>系统</dt>
      <dd>{{ state.overview?.os }}</dd>
      <dt>权限</dt>
      <dd>{{ state.overview?.simulated ? '演示模式（不会真的安装）' : state.overview?.admin ? '管理员' : '普通用户（功能受限）' }}</dd>
      <dt>日志目录</dt>
      <dd>
        <span class="mono">{{ state.overview?.logDir }}</span>
        <a style="margin-left: 8px" @click="open(state.overview?.logDir ?? '')">打开</a>
      </dd>
      <dt>磁盘</dt>
      <dd>
        <span v-for="d in state.overview?.drives ?? []" :key="d.root" class="tag" style="margin-right: 6px">{{ d.root }} 剩 {{ fmtBytes(d.freeBytes) }}</span>
      </dd>
    </dl>
  </div>
</template>
