<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from '../components/Icon.vue'
import InstallDialog from '../components/InstallDialog.vue'
import UninstallDialog from '../components/UninstallDialog.vue'
import { api } from '../api'
import { fail, pkgBusy, queued, state } from '../store'
import type { Installed, PackageView } from '../types'
import { initials, tileColor } from '../util'

const multi = computed(() => state.catalog.filter((p) => p.multi))
const installing = ref<PackageView | null>(null)
const removing = ref<{ pkg: PackageView; inst: Installed } | null>(null)

async function use(p: PackageView, inst: Installed) {
  try {
    const id = await api.activate(p.id, inst.version)
    queued(id, `正在把 ${p.name} 切换到 ${inst.version}`)
  } catch (e) {
    fail(e, '切换版本')
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>版本切换</h1>
      <div class="sub">同一个软件可以装多个版本，这里决定命令行默认用哪一个。切换只改环境变量，新开的命令行窗口生效。</div>
    </div>
  </div>

  <div class="grid" style="grid-template-columns: repeat(auto-fill, minmax(430px, 1fr)); align-items: start">
    <div v-for="p in multi" :key="p.id" class="card">
      <div class="row" style="gap: 12px">
        <div class="tile" :style="{ background: tileColor(p.id) }">{{ initials(p.name) }}</div>
        <div style="flex: 1; min-width: 0">
          <h2>{{ p.name }}</h2>
          <div class="muted small">
            <template v-if="p.homeVar">
              环境变量 <span class="mono">{{ p.homeVar }}</span> 指向当前版本
            </template>
            <template v-else>当前版本排在 PATH 最前面</template>
          </div>
        </div>
        <button v-if="pkgBusy(p.id)" class="btn sm" disabled><span class="spin" />进行中</button>
        <button v-else-if="p.releases.some((r) => !r.installed)" class="btn" @click="installing = p"><Icon name="download" />装一个版本</button>
      </div>

      <div v-if="!p.installed.length" class="faint small" style="margin-top: 12px">还没有安装</div>

      <div
        v-for="inst in p.installed"
        :key="inst.version"
        class="row"
        style="margin-top: 10px; padding: 9px 12px; border: 1px solid var(--line); border-radius: 8px"
        :style="inst.active ? { borderColor: 'var(--primary)', background: 'var(--primary-soft)' } : {}"
      >
        <span class="dot" :class="{ ok: inst.active }" />
        <strong class="mono">{{ inst.version }}</strong>
        <span v-if="inst.active" class="tag primary">当前使用</span>
        <span class="spacer" />
        <button v-if="!inst.active" class="btn sm primary" :disabled="!!pkgBusy(p.id)" @click="use(p, inst)"><Icon name="swap" :size="13" />切换到这个</button>
        <button class="btn sm danger" :disabled="!!pkgBusy(p.id)" @click="removing = { pkg: p, inst }">卸载</button>
      </div>
    </div>
  </div>

  <InstallDialog v-if="installing" :pkg="installing" @close="installing = null" />
  <UninstallDialog v-if="removing" :pkg="removing.pkg" :inst="removing.inst" @close="removing = null" />
</template>
