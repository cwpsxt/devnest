<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import Icon from '../components/Icon.vue'
import InstallDialog from '../components/InstallDialog.vue'
import UninstallDialog from '../components/UninstallDialog.vue'
import { api } from '../api'
import { fail, openLog, pkgBusy, state, toast } from '../store'
import type { Installed, PackageView } from '../types'
import { CATEGORIES, categoryName, initials, tileColor } from '../util'

type Filter = 'all' | 'installed' | PackageView['category']

const filter = ref<Filter>('all')
const query = ref('')
const installing = ref<PackageView | null>(null)
const removing = ref<{ pkg: PackageView; inst: Installed } | null>(null)
const expanded = reactive<Record<string, boolean>>({})

const installedCount = computed(() => state.catalog.filter((p) => p.installed.length).length)
const categoryRank = (p: PackageView) => CATEGORIES.findIndex((c) => c.id === p.category)

const tabs = computed<{ id: Filter; name: string }[]>(() => [
  { id: 'all', name: '全部' },
  { id: 'installed', name: `已安装 ${installedCount.value}` },
  ...CATEGORIES.filter((c) => c.id !== 'dep'),
])

const list = computed(() => {
  const q = query.value.trim().toLowerCase()
  const shown = state.catalog.filter((p) => {
    // Runtime libraries are installed automatically; list them only once present.
    if (p.category === 'dep' && !p.installed.length) return false
    if (filter.value === 'installed') {
      if (!p.installed.length) return false
    } else if (filter.value !== 'all' && p.category !== filter.value) {
      return false
    }
    return !q || p.name.toLowerCase().includes(q) || p.id.includes(q) || p.desc.toLowerCase().includes(q)
  })
  // Array.prototype.sort is stable, so the catalogue order survives inside a category.
  return shown.sort((a, b) => categoryRank(a) - categoryRank(b))
})

const canAddVersion = (p: PackageView) => p.multi && p.releases.some((r) => !r.installed)

async function copy(text: string) {
  try {
    await api.copyText(text)
    toast('ok', '已复制')
  } catch (e) {
    fail(e, '复制')
  }
}

async function openDir(path: string) {
  try {
    await api.openPath(path)
  } catch (e) {
    fail(e)
  }
}

const isUrl = (v: string) => /^https?:\/\//.test(v)

onMounted(async () => {
  const id = state.focusPkg
  if (!id) return
  state.focusPkg = ''
  expanded[id] = true
  await nextTick()
  document.getElementById('pkg-' + id)?.scrollIntoView({ block: 'center' })
})
</script>

<template>
  <div class="page-head">
    <div>
      <h1>软件库</h1>
      <div class="sub">点「安装」后会自动下载、解压、写配置、注册服务和环境变量；都装在设置里的安装目录下。</div>
    </div>
    <span class="spacer" />
    <div class="row" style="position: relative">
      <Icon name="search" style="position: absolute; left: 9px; color: var(--faint)" />
      <input v-model="query" class="input" style="width: 220px; padding-left: 30px" placeholder="搜索软件" spellcheck="false" />
    </div>
  </div>

  <div class="tabs" style="margin-bottom: 14px">
    <button v-for="t in tabs" :key="t.id" class="tab" :class="{ active: filter === t.id }" @click="filter = t.id">{{ t.name }}</button>
  </div>

  <div v-if="!list.length" class="card empty">
    <div class="big">没有符合条件的软件</div>
    <div v-if="filter === 'installed' && !query">还没有安装任何软件，切到「全部」挑一个开始。</div>
  </div>

  <div class="grid" style="grid-template-columns: repeat(auto-fill, minmax(430px, 1fr)); align-items: start">
    <div v-for="p in list" :id="'pkg-' + p.id" :key="p.id" class="card">
      <div class="row" style="align-items: flex-start; gap: 12px">
        <div class="tile" :style="{ background: tileColor(p.id) }">{{ initials(p.name) }}</div>
        <div style="flex: 1; min-width: 0">
          <div class="row wrap" style="gap: 6px">
            <h2>{{ p.name }}</h2>
            <span class="tag">{{ categoryName(p.category) }}</span>
            <span v-if="p.multi" class="tag primary" title="可以同时装多个版本并切换">多版本</span>
          </div>
          <div class="muted small" style="margin-top: 3px">{{ p.desc }}</div>
        </div>
        <div style="flex: none">
          <button v-if="pkgBusy(p.id)" class="btn sm" @click="openLog(pkgBusy(p.id)!.id)"><span class="spin" />进行中</button>
          <button v-else-if="!p.installed.length" class="btn primary" @click="installing = p"><Icon name="download" />安装</button>
          <button v-else-if="canAddVersion(p)" class="btn" @click="installing = p">装其他版本</button>
        </div>
      </div>

      <div v-if="pkgBusy(p.id)" style="margin-top: 12px">
        <div class="progress"><i :style="{ width: Math.round(pkgBusy(p.id)!.progress * 100) + '%' }" /></div>
        <div class="muted small" style="margin-top: 4px">{{ pkgBusy(p.id)!.title }} · {{ pkgBusy(p.id)!.message || '排队中' }}</div>
      </div>

      <div v-for="inst in p.installed" :key="inst.version" style="margin-top: 12px; border-top: 1px solid var(--line); padding-top: 10px">
        <div class="row wrap">
          <span class="tag ok"><Icon name="check" :size="12" />已安装 {{ inst.version }}</span>
          <span v-if="p.multi && inst.active" class="tag primary">当前使用</span>
          <span v-for="port in inst.ports ?? []" :key="port.name + port.port" class="tag">{{ port.name }} {{ port.port }}</span>
          <span class="spacer" />
          <button class="btn ghost sm" @click="expanded[p.id] = !expanded[p.id]">{{ expanded[p.id] ? '收起' : '详情' }}</button>
          <button class="btn ghost sm" title="打开安装目录" @click="openDir(inst.dir)"><Icon name="folder" /></button>
          <button class="btn danger sm" :disabled="!!pkgBusy(p.id)" @click="removing = { pkg: p, inst }">卸载</button>
        </div>

        <dl v-if="expanded[p.id]" class="kv selectable" style="margin-top: 10px">
          <template v-for="kv in inst.info ?? []" :key="kv.label">
            <dt>{{ kv.label }}</dt>
            <dd>
              <a v-if="isUrl(kv.value)" @click="api.openUrl(kv.value).catch(fail)">{{ kv.value }}</a>
              <span v-else class="mono">{{ kv.value }}</span>
              <button class="btn ghost sm" style="height: 20px; padding: 0 4px; vertical-align: middle" title="复制" @click="copy(kv.value)"><Icon name="copy" :size="13" /></button>
            </dd>
          </template>
          <dt>安装目录</dt>
          <dd class="mono">{{ inst.dir }}</dd>
          <template v-if="inst.env?.length">
            <dt>环境变量</dt>
            <dd class="mono">{{ inst.env.join('、') }}</dd>
          </template>
          <template v-if="inst.paths?.length">
            <dt>加入 PATH</dt>
            <dd class="mono">{{ inst.paths.join('；') }}</dd>
          </template>
        </dl>
      </div>

      <div v-if="!p.installed.length && p.requires.length" class="faint small" style="margin-top: 10px">需要 {{ p.requires.join('、') }}（没有会自动装）</div>
    </div>
  </div>

  <InstallDialog v-if="installing" :pkg="installing" @close="installing = null" />
  <UninstallDialog v-if="removing" :pkg="removing.pkg" :inst="removing.inst" @close="removing = null" />
</template>
