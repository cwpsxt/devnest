<script setup lang="ts">
import { ICONS } from '../icons'

withDefaults(defineProps<{ name: string; size?: number }>(), { size: 16 })

const paths: Record<string, string> = ICONS
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="paths[name]" />
  </svg>
</template>
