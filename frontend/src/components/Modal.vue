<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import Icon from './Icon.vue'

const props = withDefaults(defineProps<{ title: string; wide?: boolean; closable?: boolean }>(), {
  wide: false,
  closable: true,
})
const emit = defineEmits<{ close: [] }>()

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && props.closable) emit('close')
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="overlay" @mousedown.self="closable && emit('close')">
    <div class="modal" :class="{ wide }" role="dialog" aria-modal="true" :aria-label="title">
      <div class="modal-head">
        <slot name="icon" />
        <h2>{{ title }}</h2>
        <span class="spacer" />
        <slot name="head" />
        <button v-if="closable" class="btn ghost sm" title="关闭" @click="emit('close')">
          <Icon name="x" />
        </button>
      </div>
      <div class="modal-body">
        <slot />
      </div>
      <div v-if="$slots.foot" class="modal-foot">
        <slot name="foot" />
      </div>
    </div>
  </div>
</template>
