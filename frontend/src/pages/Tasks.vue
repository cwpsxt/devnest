<script setup lang="ts">
import { computed } from 'vue'
import Icon from '../components/Icon.vue'
import { api } from '../api'
import { fail, openLog, refreshTasks, state } from '../store'
import type { Task } from '../types'
import { TASK_STATES, fmtTime } from '../util'

// The running task first, then the queue in the order it will run, then history newest first.
const tasks = computed(() => {
  const rank = (t: Task) => (t.state === 'running' ? 0 : t.state === 'queued' ? 1 : 2)
  return [...state.tasks].sort((a, b) => rank(a) - rank(b) || (a.state === 'queued' ? a.id - b.id : b.id - a.id))
})

const finished = computed(() => state.tasks.filter((t) => t.state !== 'running' && t.state !== 'queued').length)

async function cancel(t: Task) {
  try {
    await api.cancelTask(t.id)
  } catch (e) {
    fail(e, '取消任务')
  }
}

async function clear() {
  try {
    await api.clearTasks()
    await refreshTasks()
  } catch (e) {
    fail(e)
  }
}

async function openLogDir() {
  try {
    await api.openPath(state.overview?.logDir ?? '')
  } catch (e) {
    fail(e)
  }
}

function duration(t: Task): string {
  const a = Date.parse(t.started)
  const b = Date.parse(t.ended)
  if (!fmtTime(t.started) || !fmtTime(t.ended) || isNaN(a) || isNaN(b)) return ''
  const s = Math.max(0, Math.round((b - a) / 1000))
  return s < 60 ? `${s} 秒` : `${Math.floor(s / 60)} 分 ${s % 60} 秒`
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>任务与日志</h1>
      <div class="sub">安装、卸载、清理都按顺序一个一个执行。每个任务的完整日志也写在日志目录里。</div>
    </div>
    <span class="spacer" />
    <button class="btn" @click="openLogDir"><Icon name="folder" />打开日志目录</button>
    <button class="btn" :disabled="!finished" @click="clear">清除已结束的记录</button>
  </div>

  <div v-if="!tasks.length" class="card empty">
    <div class="big">还没有任务</div>
    <div>安装或清理开始后，进度和日志会出现在这里。</div>
  </div>

  <div v-for="t in tasks" :key="t.id" class="card">
    <div class="row">
      <span v-if="t.state === 'running'" class="spin" />
      <span v-else class="dot" :class="{ ok: t.state === 'done', danger: t.state === 'failed', warn: t.state === 'queued' }" />
      <strong>{{ t.title }}</strong>
      <span class="tag" :class="{ ok: t.state === 'done', danger: t.state === 'failed', primary: t.state === 'running' }">{{ TASK_STATES[t.state] }}</span>
      <span class="spacer" />
      <span class="faint small">{{ fmtTime(t.ended) || fmtTime(t.started) }}<template v-if="duration(t)"> · 用时 {{ duration(t) }}</template></span>
      <button class="btn sm" @click="openLog(t.id)"><Icon name="file" :size="13" />日志</button>
      <button v-if="t.state === 'running' || t.state === 'queued'" class="btn sm danger" @click="cancel(t)">取消</button>
    </div>
    <template v-if="t.state === 'running'">
      <div class="progress" style="margin-top: 10px"><i :style="{ width: Math.round(t.progress * 100) + '%' }" /></div>
      <div class="muted small" style="margin-top: 4px">{{ Math.round(t.progress * 100) }}% · {{ t.message }}</div>
    </template>
    <div v-else-if="t.state === 'failed'" class="small selectable" style="color: var(--danger); margin-top: 6px">{{ t.error }}</div>
    <div v-else-if="t.state === 'done' && t.message && t.message !== TASK_STATES.done" class="muted small" style="margin-top: 6px">{{ t.message }}</div>
  </div>
</template>
