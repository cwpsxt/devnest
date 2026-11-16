<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import Modal from './Modal.vue'
import Icon from './Icon.vue'
import { api, errText } from '../api'
import { fail, queued, refreshCatalog, state, toast } from '../store'
import type { Option, PackageView, PortRow } from '../types'
import { fmtBytes, randomPassword, tileColor, initials } from '../util'

const props = defineProps<{ pkg: PackageView; version?: string }>()
const emit = defineEmits<{ close: [] }>()

// The catalogue may refresh while the dialog is open; keep reading the live copy.
const pkg = computed(() => state.catalog.find((p) => p.id === props.pkg.id) ?? props.pkg)
const choices = computed(() => pkg.value.releases.filter((r) => !r.installed))
const version = ref(props.version && choices.value.some((r) => r.version === props.version) ? props.version : choices.value[0]?.version ?? '')
const release = computed(() => pkg.value.releases.find((r) => r.version === version.value))

const values = reactive<Record<string, string>>({})
for (const o of pkg.value.options) values[o.key] = o.default

const shown = reactive<Record<string, boolean>>({})
const ports = reactive<Record<string, PortRow | null>>({})
const submitting = ref(false)
const serverError = ref('')

const lan = computed(() => values.lan === 'true')

/** The same rules as CheckOptions on the Go side, so mistakes show while typing. */
function problem(o: Option): string {
  const v = (values[o.key] ?? '').trim()
  if (o.type === 'port') {
    const n = Number(v)
    if (!/^\d+$/.test(v) || n < 1 || n > 65535) return '端口必须是 1–65535 之间的数字'
    const twin = pkg.value.options.find((x) => x.type === 'port' && x.key !== o.key && (values[x.key] ?? '').trim() === v)
    if (twin) return `和「${twin.label}」用了同一个端口`
  }
  if (o.type === 'password') {
    if (v.length < (o.minLen ?? 0)) return `至少 ${o.minLen} 位`
    if (v.length > 32) return '最多 32 位'
    if (v && !/^[A-Za-z0-9_@#.+\-]+$/.test(v)) return '只能用字母、数字和 _ @ # . + - 这几个符号'
    if (lan.value && (v === o.default || v.length < 8)) return '允许局域网访问时，密码不能用默认值，并且至少 8 位'
  }
  if (o.type === 'text' && o.pattern) {
    try {
      if (!new RegExp(o.pattern).test(v)) return '格式不对'
    } catch {
      // a pattern the browser cannot compile is left to the Go side
    }
  }
  return ''
}

const firstProblem = computed(() => pkg.value.options.map(problem).find((p) => p))
const portTaken = computed(() => Object.values(ports).some((p) => p?.listening))

async function checkPort(o: Option) {
  const n = Number(values[o.key])
  if (problem(o) || !n) {
    ports[o.key] = null
    return
  }
  try {
    ports[o.key] = await api.portInfo(n)
  } catch {
    ports[o.key] = null
  }
}

for (const o of pkg.value.options) if (o.type === 'port') checkPort(o)

watch(version, () => (serverError.value = ''))

async function importFile() {
  if (!release.value) return
  try {
    const path = await api.importInstaller(pkg.value.id, release.value.version)
    if (path) {
      toast('ok', '安装包已校验并导入，安装时不用再下载')
      await refreshCatalog()
    }
  } catch (e) {
    fail(e, '导入安装包')
  }
}

async function submit() {
  if (!release.value || firstProblem.value) return
  submitting.value = true
  serverError.value = ''
  try {
    const options: Record<string, string> = {}
    for (const o of pkg.value.options) options[o.key] = (values[o.key] ?? '').trim()
    const ids = await api.install({ id: pkg.value.id, version: release.value.version, options })
    queued(ids, `${pkg.value.name} ${release.value.version} 已加入安装队列` + (ids.length > 1 ? `（连同依赖共 ${ids.length} 个任务）` : ''))
    emit('close')
  } catch (e) {
    serverError.value = errText(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal :title="'安装 ' + pkg.name" @close="emit('close')">
    <template #icon>
      <div class="tile" :style="{ background: tileColor(pkg.id), width: '30px', height: '30px', fontSize: '12px', borderRadius: '8px' }">{{ initials(pkg.name) }}</div>
    </template>

    <div v-if="!choices.length" class="note">这个软件的所有版本都已经装上了。</div>

    <template v-else>
      <div class="field">
        <label>版本</label>
        <select v-model="version" class="input" style="width: 100%">
          <option v-for="r in choices" :key="r.version" :value="r.version">{{ r.label }}</option>
        </select>
        <div v-if="release" class="help row wrap">
          <span v-if="release.cached" class="tag ok"><Icon name="check" :size="12" />安装包已在本机</span>
          <template v-else>
            <span>需要下载 {{ release.size > 0 ? fmtBytes(release.size) : '（大小未知）' }}，首选来源 {{ release.source }}</span>
            <a @click="importFile">我已经下载好了，选择文件</a>
          </template>
        </div>
      </div>

      <div v-for="o in pkg.options" :key="o.key" class="field">
        <template v-if="o.type === 'bool'">
          <label class="row" style="font-weight: 400; cursor: pointer; margin: 0">
            <input type="checkbox" class="check" :checked="values[o.key] === 'true'" @change="values[o.key] = ($event.target as HTMLInputElement).checked ? 'true' : 'false'" />
            {{ o.label }}
          </label>
        </template>

        <template v-else>
          <label :for="'opt-' + o.key">{{ o.label }}</label>
          <div class="row">
            <select v-if="o.type === 'select'" :id="'opt-' + o.key" v-model="values[o.key]" class="input" style="flex: 1">
              <option v-for="c in o.choices" :key="c.value" :value="c.value">{{ c.label }}</option>
            </select>

            <template v-else-if="o.type === 'password'">
              <input
                :id="'opt-' + o.key"
                v-model="values[o.key]"
                class="input mono"
                :class="{ invalid: problem(o) }"
                style="flex: 1"
                :type="shown[o.key] ? 'text' : 'password'"
                autocomplete="new-password"
                spellcheck="false"
              />
              <button class="btn" type="button" @click="shown[o.key] = !shown[o.key]">{{ shown[o.key] ? '隐藏' : '显示' }}</button>
              <button class="btn" type="button" title="生成一个随机密码并显示出来" @click="((values[o.key] = randomPassword()), (shown[o.key] = true))">随机</button>
            </template>

            <template v-else-if="o.type === 'port'">
              <input
                :id="'opt-' + o.key"
                v-model="values[o.key]"
                class="input mono"
                :class="{ invalid: problem(o) || ports[o.key]?.listening }"
                style="width: 120px"
                inputmode="numeric"
                maxlength="5"
                @change="checkPort(o)"
                @blur="checkPort(o)"
              />
              <span v-if="ports[o.key]?.listening" class="tag danger">
                已被 {{ ports[o.key]?.owner || '其他程序' }}（PID {{ ports[o.key]?.ownerPid }}）占用
              </span>
              <span v-else-if="ports[o.key] && !problem(o)" class="tag ok">空闲</span>
            </template>

            <input v-else :id="'opt-' + o.key" v-model="values[o.key]" class="input" :class="{ invalid: problem(o) }" style="flex: 1" spellcheck="false" />
          </div>
        </template>

        <div v-if="problem(o)" class="error">{{ problem(o) }}</div>
        <div v-if="o.help" class="help">{{ o.help }}</div>
      </div>

      <div v-if="pkg.requires.length" class="note" style="margin-bottom: 10px">
        需要：{{ pkg.requires.join('、') }}。本机还没有的话会先自动装上。
      </div>

      <div v-if="pkg.notes.length" class="note" style="margin-bottom: 10px">
        <ul>
          <li v-for="(n, i) in pkg.notes" :key="i">{{ n }}</li>
        </ul>
      </div>

      <div v-if="portTaken" class="note warn" style="margin-bottom: 10px">有端口已被占用。换一个端口，或者先到「环境清理」把旧的安装清掉。</div>
      <div v-if="serverError" class="note danger selectable">{{ serverError }}</div>
    </template>

    <template #foot>
      <button class="btn" @click="emit('close')">取消</button>
      <button class="btn primary" :disabled="!release || !!firstProblem || submitting" @click="submit">
        <span v-if="submitting" class="spin" />
        开始安装
      </button>
    </template>
  </Modal>
</template>
