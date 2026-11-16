<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import Icon from '../components/Icon.vue'
import { api } from '../api'
import { confirmDialog, fail, toast } from '../store'
import type { MirrorItem } from '../types'

const OFFICIAL = ''
const CUSTOM = '__custom__'

const items = ref<MirrorItem[]>([])
const loading = ref(true)
/** what the select shows for each item: a mirror URL, OFFICIAL or CUSTOM */
const picked = reactive<Record<string, string>>({})
const customUrl = reactive<Record<string, string>>({})
const saving = reactive<Record<string, boolean>>({})
const applyingAll = ref(false)

function sync(list: MirrorItem[]) {
  items.value = list
  for (const it of list) {
    const known = it.current === OFFICIAL || it.choices.some((c) => c.value === it.current)
    picked[it.id] = known ? it.current : CUSTOM
    customUrl[it.id] = known ? '' : it.current
  }
}

async function load() {
  try {
    sync(await api.mirrors())
  } catch (e) {
    fail(e, '读取镜像设置')
  } finally {
    loading.value = false
  }
}

onMounted(load)

const wanted = (it: MirrorItem) => (picked[it.id] === CUSTOM ? (customUrl[it.id] ?? '').trim() : picked[it.id] ?? '')
const dirty = (it: MirrorItem) => wanted(it) !== it.current
const label = (it: MirrorItem) => it.choices.find((c) => c.value === it.current)?.label ?? (it.current ? '自定义地址' : '官方源')

async function apply(it: MirrorItem) {
  const url = wanted(it)
  if (picked[it.id] === CUSTOM && !url) {
    toast('error', '请先填写自定义地址')
    return
  }
  saving[it.id] = true
  try {
    await api.setMirror(it.id, url)
    toast('ok', `${it.name} 已${url ? '切换到镜像' : '恢复官方源'}`)
    await load()
  } catch (e) {
    fail(e, it.name)
  } finally {
    saving[it.id] = false
  }
}

async function applyAll() {
  const ok = await confirmDialog({
    title: '全部设为国内镜像',
    message: '会把下面每一项都改成列表里的第一个推荐镜像，已经手动设过的也会被覆盖。没有推荐镜像的项（如 Docker）不动。',
    details: items.value.filter((i) => i.choices.length).map((i) => `${i.name} → ${i.choices[0].label}`),
    okText: '全部设置',
  })
  if (!ok) return
  applyingAll.value = true
  try {
    const problems = await api.setAllMirrors()
    if (problems.length) toast('error', '有几项没有设置成功：' + problems.join('；'))
    else toast('ok', '已全部切换到国内镜像')
    await load()
  } catch (e) {
    fail(e)
  } finally {
    applyingAll.value = false
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>镜像加速</h1>
      <div class="sub">把 npm、pip、Maven 等工具的下载源换成国内镜像。只改当前用户的配置文件，随时可以恢复官方源。</div>
    </div>
    <span class="spacer" />
    <button class="btn primary" :disabled="applyingAll || loading" @click="applyAll"><Icon name="zap" />全部设为国内镜像</button>
  </div>

  <div v-if="loading" class="card empty"><span class="spin" style="display: inline-block" /></div>

  <div v-for="it in items" :key="it.id" class="card">
    <div class="row wrap">
      <h2>{{ it.name }}</h2>
      <span class="tag" :class="it.current ? 'ok' : ''">{{ label(it) }}</span>
    </div>
    <div class="muted small" style="margin: 3px 0 10px">{{ it.desc }}</div>
    <div class="row wrap">
      <select v-model="picked[it.id]" class="input" style="width: 260px">
        <option v-for="c in it.choices" :key="c.value" :value="c.value">{{ c.label }}</option>
        <option :value="OFFICIAL">官方源（不用镜像）</option>
        <option v-if="it.custom" :value="CUSTOM">自定义地址…</option>
      </select>
      <input
        v-if="picked[it.id] === CUSTOM"
        v-model="customUrl[it.id]"
        class="input mono"
        style="flex: 1; min-width: 260px"
        placeholder="https://"
        spellcheck="false"
        @keydown.enter="apply(it)"
      />
      <span v-else-if="picked[it.id]" class="mono faint selectable" style="flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">{{ picked[it.id] }}</span>
      <span v-else class="spacer" />
      <button class="btn" :class="{ primary: dirty(it) }" :disabled="!dirty(it) || saving[it.id]" @click="apply(it)">应用</button>
    </div>
    <div class="faint small mono selectable" style="margin-top: 8px">配置文件：{{ it.file }}</div>
  </div>
</template>
