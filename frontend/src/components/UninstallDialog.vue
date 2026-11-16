<script setup lang="ts">
import { ref } from 'vue'
import Modal from './Modal.vue'
import { api, errText } from '../api'
import { queued } from '../store'
import type { Installed, PackageView } from '../types'

const props = defineProps<{ pkg: PackageView; inst: Installed }>()
const emit = defineEmits<{ close: [] }>()

const hasData = props.pkg.hasData && !!props.inst.dataDir
const mode = ref<'keep' | 'backup' | 'delete'>('keep')
const sure = ref(false)
const submitting = ref(false)
const error = ref('')

async function submit() {
  submitting.value = true
  error.value = ''
  try {
    const id = await api.uninstall(props.pkg.id, props.inst.version, hasData ? mode.value : 'keep')
    queued(id, `${props.pkg.name} ${props.inst.version} 已加入卸载队列`)
    emit('close')
  } catch (e) {
    error.value = errText(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal :title="`卸载 ${pkg.name} ${inst.version}`" @close="emit('close')">
    <p>会停止并删除它的服务、删除安装目录，并撤掉它加的环境变量、PATH 和快捷方式。</p>
    <dl class="kv selectable" style="margin: 12px 0">
      <dt>安装目录</dt>
      <dd class="mono">{{ inst.dir }}</dd>
      <template v-if="hasData">
        <dt>数据目录</dt>
        <dd class="mono">{{ inst.dataDir }}</dd>
      </template>
    </dl>

    <div v-if="hasData" class="field">
      <label>数据怎么处理</label>
      <label class="row" style="font-weight: 400; cursor: pointer; margin-bottom: 6px">
        <input v-model="mode" type="radio" value="keep" class="check" />
        保留在原处（以后重装同一系列版本会接着用）
      </label>
      <label class="row" style="font-weight: 400; cursor: pointer; margin-bottom: 6px">
        <input v-model="mode" type="radio" value="backup" class="check" />
        移到备份目录（重装时是全新的，旧数据还能手动找回）
      </label>
      <label class="row" style="font-weight: 400; cursor: pointer">
        <input v-model="mode" type="radio" value="delete" class="check" />
        <span style="color: var(--danger)">彻底删除（无法恢复）</span>
      </label>
      <label v-if="mode === 'delete'" class="note danger row" style="margin-top: 10px; cursor: pointer">
        <input v-model="sure" type="checkbox" class="check" />
        我确认要删除 {{ inst.dataDir }} 里的全部数据
      </label>
    </div>

    <div v-if="error" class="note danger selectable">{{ error }}</div>

    <template #foot>
      <button class="btn" @click="emit('close')">取消</button>
      <button class="btn danger solid" :disabled="submitting || (hasData && mode === 'delete' && !sure)" @click="submit">卸载</button>
    </template>
  </Modal>
</template>
