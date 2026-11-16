<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import Modal from './Modal.vue'
import { api } from '../api'
import { closeLog, fail, state, toast } from '../store'
import { TASK_STATES } from '../util'

const box = ref<HTMLElement>()
const follow = ref(true)

const task = computed(() => state.tasks.find((t) => t.id === state.logTask))
const lines = computed(() => state.logs[state.logTask] ?? [])
const active = computed(() => task.value?.state === 'running' || task.value?.state === 'queued')

function onScroll() {
  const el = box.value
  if (el) follow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

watch(
  () => lines.value.length,
  async () => {
    if (!follow.value) return
    await nextTick()
    if (box.value) box.value.scrollTop = box.value.scrollHeight
  },
  { immediate: true },
)

async function copy() {
  try {
    await api.copyText(lines.value.join('\n'))
    toast('ok', '日志已复制')
  } catch (e) {
    fail(e, '复制')
  }
}

async function cancel() {
  if (!task.value) return
  try {
    await api.cancelTask(task.value.id)
  } catch (e) {
    fail(e, '取消任务')
  }
}
</script>

<template>
  <Modal :title="task?.title ?? '任务日志'" wide @close="closeLog">
    <template #head>
      <span v-if="task" class="tag" :class="{ ok: task.state === 'done', danger: task.state === 'failed', primary: task.state === 'running' }">
        {{ TASK_STATES[task.state] }}
      </span>
    </template>
    <div v-if="task?.state === 'failed' && task.error" class="note danger selectable" style="margin-bottom: 10px">{{ task.error }}</div>
    <pre ref="box" class="logbox" @scroll="onScroll">{{ lines.length ? lines.join('\n') : '（还没有输出）' }}</pre>
    <template #foot>
      <button v-if="active" class="btn danger" @click="cancel">取消这个任务</button>
      <span class="spacer" />
      <button class="btn" @click="copy">复制日志</button>
      <button class="btn primary" @click="closeLog">关闭</button>
    </template>
  </Modal>
</template>
