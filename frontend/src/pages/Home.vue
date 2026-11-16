<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import { api, errText } from '../api'
import { busy, go, refreshTasks, state, toast } from '../store'
import type { PackageView, Preset, ReleaseView } from '../types'
import { fmtBytes, initials, tileColor } from '../util'

interface PresetItem {
  pkg: PackageView
  release: ReleaseView
  /** an installed version that already satisfies this item */
  have: string
}

/** "jdk@21" means the newest JDK whose version starts with 21. */
function resolve(item: string): PresetItem | null {
  const [id, prefix] = item.split('@')
  const pkg = state.catalog.find((p) => p.id === id)
  if (!pkg) return null
  const fits = (v: string) => !prefix || v === prefix || v.startsWith(prefix + '.')
  const release = pkg.releases.find((r) => fits(r.version))
  if (!release) return null
  const have = pkg.installed.find((i) => (pkg.multi ? fits(i.version) : true))?.version ?? ''
  return { pkg, release, have }
}

const presets = computed(() =>
  state.presets.map((p) => {
    const items = p.items.map(resolve).filter((x): x is PresetItem => !!x)
    return { preset: p, items, missing: items.filter((i) => !i.have) }
  }),
)

const rootDrive = computed(() => {
  const o = state.overview
  if (!o) return null
  return o.drives.find((d) => o.root.toLowerCase().startsWith(d.root.toLowerCase())) ?? null
})

const recent = computed(() =>
  state.catalog
    .filter((p) => p.category !== 'dep')
    .flatMap((p) => p.installed.map((inst) => ({ pkg: p, inst })))
    .sort((a, b) => (a.inst.at < b.inst.at ? 1 : -1))
    .slice(0, 8),
)

// ---- preset dialog ----

const chosen = ref<{ preset: Preset; items: PresetItem[] } | null>(null)
const picked = reactive<Record<string, boolean>>({})
const submitting = ref(false)
const result = ref<{ errors: string[]; skipped: string[] } | null>(null)

function openPreset(p: { preset: Preset; items: PresetItem[] }) {
  chosen.value = p
  result.value = null
  for (const it of p.items) picked[it.pkg.id] = !it.have
}

const toInstall = computed(() => chosen.value?.items.filter((i) => !i.have && picked[i.pkg.id]) ?? [])
const totalSize = computed(() => toInstall.value.reduce((n, i) => n + (i.release.cached ? 0 : Math.max(i.release.size, 0)), 0))

/** Default passwords the bundle will set, so nobody has to guess them later. */
const defaults = computed(() =>
  toInstall.value.flatMap((i) =>
    i.pkg.options.filter((o) => o.type === 'password').map((o) => `${i.pkg.name} 的 ${o.label}：${o.default || '不设密码'}`),
  ),
)

async function installPreset() {
  if (!toInstall.value.length) return
  submitting.value = true
  try {
    const res = await api.installMany(toInstall.value.map((i) => ({ id: i.pkg.id, version: i.release.version, options: {} })))
    await refreshTasks()
    if (res.errors.length) {
      result.value = { errors: res.errors, skipped: res.skipped }
    } else {
      toast('info', `已加入 ${res.tasks.length} 个安装任务，可以在底部看进度`)
      chosen.value = null
    }
  } catch (e) {
    result.value = { errors: [errText(e)], skipped: [] }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>概览</h1>
      <div class="sub">一键装好开发环境，或者先把电脑里旧的安装清理干净。</div>
    </div>
  </div>

  <div class="grid" style="grid-template-columns: repeat(4, 1fr); margin-bottom: 14px">
    <div class="card" style="cursor: pointer" @click="go('software')">
      <div class="muted small">已安装软件</div>
      <div style="font-size: 26px; font-weight: 600">{{ state.overview?.installed }}</div>
      <div class="faint small">共 {{ state.catalog.filter((p) => p.category !== 'dep').length }} 款可装</div>
    </div>
    <div class="card" style="cursor: pointer" @click="go('services')">
      <div class="muted small">服务</div>
      <div style="font-size: 26px; font-weight: 600">
        {{ state.overview?.running }}<span class="faint" style="font-size: 15px; font-weight: 400"> / {{ state.overview?.services }}</span>
      </div>
      <div class="faint small">正在运行 / 全部</div>
    </div>
    <div class="card" style="cursor: pointer; min-width: 0" @click="go('settings')">
      <div class="muted small">安装目录</div>
      <div class="mono selectable" style="font-size: 15px; font-weight: 600; margin: 7px 0 6px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap" :title="state.overview?.root">
        {{ state.overview?.root }}
      </div>
      <div class="faint small">{{ rootDrive ? `所在磁盘还剩 ${fmtBytes(rootDrive.freeBytes)}` : '—' }}</div>
    </div>
    <div class="card" style="cursor: pointer" @click="go('settings')">
      <div class="muted small">安装包缓存</div>
      <div style="font-size: 26px; font-weight: 600">{{ state.overview?.cacheSize ? fmtBytes(state.overview.cacheSize) : '空' }}</div>
      <div class="faint small">可以在设置里清空</div>
    </div>
  </div>

  <div class="card" style="display: flex; align-items: center; gap: 14px">
    <div class="tile" style="background: #f59e0b"><Icon name="trash" :size="20" /></div>
    <div style="flex: 1">
      <h2>电脑里已经装过这些软件？先清理</h2>
      <div class="muted small">扫描旧的 MySQL、JDK、Node.js 等安装留下的服务、目录、环境变量和注册表项。先列出来给你看，确认后才动手，数据目录会先备份。</div>
    </div>
    <button class="btn lg" @click="go('clean')">去扫描</button>
  </div>

  <h2 style="margin: 22px 0 10px">一键装好一套环境</h2>
  <div class="grid" style="grid-template-columns: repeat(auto-fill, minmax(300px, 1fr))">
    <div v-for="p in presets" :key="p.preset.id" class="card" style="display: flex; flex-direction: column">
      <div class="row">
        <h2>{{ p.preset.name }}</h2>
        <span class="spacer" />
        <span v-if="!p.missing.length" class="tag ok"><Icon name="check" :size="12" />已装齐</span>
        <span v-else-if="p.missing.length < p.items.length" class="tag">还差 {{ p.missing.length }} 个</span>
      </div>
      <div class="row wrap" style="gap: 6px; margin: 10px 0 14px">
        <span v-for="it in p.items" :key="it.pkg.id" class="tag" :class="{ ok: it.have }" :title="it.have ? '已安装 ' + it.have : '将安装 ' + it.release.version">
          <Icon v-if="it.have" name="check" :size="12" />{{ it.pkg.name }}
        </span>
      </div>
      <span class="spacer" />
      <button class="btn" :class="{ primary: p.missing.length }" :disabled="!p.missing.length" @click="openPreset(p)">
        {{ p.missing.length ? '一键安装' : '都装好了' }}
      </button>
    </div>
  </div>

  <template v-if="recent.length">
    <h2 style="margin: 22px 0 10px">已安装</h2>
    <div class="card" style="padding: 6px 8px">
      <table class="table">
        <tbody>
          <tr v-for="r in recent" :key="r.pkg.id + r.inst.version" style="cursor: pointer" @click="go('software', r.pkg.id)">
            <td style="width: 44px">
              <div class="tile" :style="{ background: tileColor(r.pkg.id), width: '30px', height: '30px', fontSize: '12px', borderRadius: '8px' }">{{ initials(r.pkg.name) }}</div>
            </td>
            <td>
              <strong>{{ r.pkg.name }}</strong> <span class="muted">{{ r.inst.version }}</span>
            </td>
            <td class="muted small">
              <span v-for="port in r.inst.ports ?? []" :key="port.name + port.port" class="tag" style="margin-right: 4px">{{ port.name }} {{ port.port }}</span>
            </td>
            <td class="mono muted selectable" style="text-align: right">{{ r.inst.dir }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </template>

  <Modal v-if="chosen" :title="'安装「' + chosen.preset.name + '」套装'" @close="chosen = null">
    <p class="muted" style="margin-bottom: 10px">勾选要装的软件。每个软件都用默认设置，只允许本机访问；想改端口或密码，请到「软件库」里单独安装。</p>
    <label v-for="it in chosen.items" :key="it.pkg.id" class="row" style="padding: 7px 0; border-bottom: 1px solid var(--line)" :style="{ cursor: it.have ? 'default' : 'pointer' }">
      <input v-model="picked[it.pkg.id]" type="checkbox" class="check" :disabled="!!it.have" />
      <strong>{{ it.pkg.name }}</strong>
      <span class="muted">{{ it.have || it.release.version }}</span>
      <span class="spacer" />
      <span v-if="it.have" class="tag ok">已安装</span>
      <span v-else-if="it.release.cached" class="tag ok">安装包已在本机</span>
      <span v-else class="faint small">{{ it.release.size > 0 ? fmtBytes(it.release.size) : '' }}</span>
    </label>

    <div class="note" style="margin-top: 12px">
      将安装 {{ toInstall.length }} 个软件<template v-if="totalSize">，需要下载约 {{ fmtBytes(totalSize) }}</template>。会按顺序一个一个装，缺的依赖会自动补上。
    </div>
    <div v-if="defaults.length" class="note warn selectable" style="margin-top: 10px">
      默认账号密码（很弱，只适合本机开发）：
      <ul>
        <li v-for="d in defaults" :key="d">{{ d }}</li>
      </ul>
    </div>
    <div v-if="result?.errors.length" class="note danger selectable" style="margin-top: 10px">
      有软件没能加入队列：
      <ul>
        <li v-for="e in result.errors" :key="e">{{ e }}</li>
      </ul>
    </div>

    <template #foot>
      <span v-if="busy" class="muted small" style="margin-right: auto">已有任务在进行，新的会排在后面</span>
      <button class="btn" @click="chosen = null">{{ result ? '关闭' : '取消' }}</button>
      <button class="btn primary" :disabled="!toInstall.length || submitting" @click="installPreset">安装 {{ toInstall.length }} 个</button>
    </template>
  </Modal>
</template>
