<script setup lang="ts">
import { computed, onMounted } from 'vue'
import Icon from './components/Icon.vue'
import Modal from './components/Modal.vue'
import TaskLog from './components/TaskLog.vue'
import Home from './pages/Home.vue'
import Software from './pages/Software.vue'
import Services from './pages/Services.vue'
import Versions from './pages/Versions.vue'
import Mirrors from './pages/Mirrors.vue'
import Clean from './pages/Clean.vue'
import Tasks from './pages/Tasks.vue'
import Settings from './pages/Settings.vue'
import { activeTasks, answerConfirm, dismissToast, go, openLog, runningTask, start, state } from './store'
import type { Page } from './store'

const NAV: { id: Page; name: string; icon: string }[] = [
  { id: 'home', name: '概览', icon: 'home' },
  { id: 'software', name: '软件库', icon: 'box' },
  { id: 'services', name: '服务面板', icon: 'server' },
  { id: 'versions', name: '版本切换', icon: 'swap' },
  { id: 'mirrors', name: '镜像加速', icon: 'zap' },
  { id: 'clean', name: '环境清理', icon: 'trash' },
  { id: 'tasks', name: '任务与日志', icon: 'list' },
  { id: 'settings', name: '设置', icon: 'settings' },
]

const pages = { home: Home, software: Software, services: Services, versions: Versions, mirrors: Mirrors, clean: Clean, tasks: Tasks, settings: Settings }
const current = computed(() => pages[state.page])
const dockTask = computed(() => runningTask.value ?? activeTasks.value[0])
const waiting = computed(() => activeTasks.value.length - (runningTask.value ? 1 : 0))

onMounted(start)
</script>

<template>
  <div v-if="state.fatal" class="center-screen">
    <div>
      <h1 style="margin-bottom: 8px; color: var(--text)">DevNest 没能启动</h1>
      <p class="selectable">{{ state.fatal }}</p>
    </div>
  </div>

  <div v-else-if="!state.ready" class="center-screen">
    <div class="row"><span class="spin" /> 正在读取本机状态…</div>
  </div>

  <div v-else class="shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">D</div>
        <div>
          <div class="brand-name">DevNest</div>
          <div class="brand-sub">开发环境管家</div>
        </div>
      </div>
      <button v-for="n in NAV" :key="n.id" class="nav-item" :class="{ active: state.page === n.id }" @click="go(n.id)">
        <Icon :name="n.icon" :size="17" />
        {{ n.name }}
        <span v-if="n.id === 'tasks' && activeTasks.length" class="nav-badge">{{ activeTasks.length }}</span>
      </button>
      <div class="sidebar-foot">
        <div>版本 {{ state.overview?.version }}</div>
        <div>{{ state.overview?.os }}</div>
      </div>
    </aside>

    <main class="main">
      <div v-if="state.overview?.simulated" class="banner warn">
        <Icon name="info" />
        演示模式：当前不是 Windows，所有安装、服务和清理都是模拟的，不会真的装任何软件。
      </div>
      <div v-else-if="!state.overview?.admin" class="banner danger">
        <Icon name="alert" />
        当前没有管理员权限，安装服务和修改系统环境变量会失败。请关闭后右键「以管理员身份运行」。
      </div>
      <div v-if="state.overview?.rootError" class="banner danger">
        <Icon name="alert" />
        安装目录不可用：{{ state.overview.rootError }}
        <a @click="go('settings')">去设置里改</a>
      </div>

      <div class="page">
        <component :is="current" />
      </div>

      <div v-if="dockTask" class="dock">
        <span class="spin" />
        <strong>{{ dockTask.title }}</strong>
        <div class="progress"><i :style="{ width: Math.round(dockTask.progress * 100) + '%' }" /></div>
        <span class="muted msg">{{ dockTask.message }}</span>
        <span class="spacer" />
        <span v-if="waiting > 0" class="tag">还有 {{ waiting }} 个在排队</span>
        <button class="btn sm" @click="openLog(dockTask.id)">看日志</button>
      </div>
    </main>

    <TaskLog v-if="state.logTask" />

    <Modal v-if="state.confirm" :title="state.confirm.title" @close="answerConfirm(false)">
      <p class="selectable" style="white-space: pre-line">{{ state.confirm.message }}</p>
      <div v-if="state.confirm.details?.length" class="note selectable" style="margin-top: 12px; max-height: 240px; overflow: auto">
        <ul>
          <li v-for="(d, i) in state.confirm.details" :key="i">{{ d }}</li>
        </ul>
      </div>
      <template #foot>
        <button class="btn" @click="answerConfirm(false)">取消</button>
        <button class="btn" :class="state.confirm.danger ? 'danger solid' : 'primary'" @click="answerConfirm(true)">
          {{ state.confirm.okText || '确定' }}
        </button>
      </template>
    </Modal>
  </div>

  <div class="toasts">
    <div v-for="t in state.toasts" :key="t.id" class="toast" :class="t.kind">
      <span>{{ t.text }}</span>
      <button class="x" title="关闭" @click="dismissToast(t.id)">×</button>
    </div>
  </div>
</template>
