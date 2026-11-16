<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import Icon from '../components/Icon.vue'
import Modal from '../components/Modal.vue'
import { api, errText } from '../api'
import { activeTasks, confirmDialog, fail, go, queued, refreshServices, state, toast } from '../store'
import type { PortRow, ServiceRow } from '../types'
import { SERVICE_STATES } from '../util'

// Services can stop on their own (a crash, another tool), so the table is
// re-read every few seconds while this page is open.
let timer = 0
onMounted(() => {
  refreshServices().catch(() => {})
  timer = window.setInterval(() => refreshServices().catch(() => {}), 4000)
})
onBeforeUnmount(() => clearInterval(timer))

const serviceBusy = (name: string) => activeTasks.value.some((t) => t.kind === 'service' && t.title.endsWith(' ' + name))

async function act(s: ServiceRow, action: 'start' | 'stop' | 'restart') {
  try {
    const id = await api.serviceAction(s.name, action)
    queued(id, { start: '正在启动 ', stop: '正在停止 ', restart: '正在重启 ' }[action] + s.name)
  } catch (e) {
    fail(e)
  }
}

async function setAuto(s: ServiceRow) {
  try {
    await api.setAutoStart(s.name, s.startType !== 'auto')
    await refreshServices()
  } catch (e) {
    fail(e, '修改启动方式')
  }
}

async function openDir(path: string) {
  try {
    await api.openPath(path)
  } catch (e) {
    fail(e)
  }
}

// ---- log viewer ----

const logOf = ref<ServiceRow | null>(null)
const logPath = ref('')
const logText = ref('')
const logError = ref('')
const logLoading = ref(false)
const logBox = ref<HTMLElement>()

async function loadLog() {
  if (!logPath.value) return
  logLoading.value = true
  logError.value = ''
  try {
    logText.value = await api.readLog(logPath.value)
    requestAnimationFrame(() => {
      if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
    })
  } catch (e) {
    logText.value = ''
    logError.value = errText(e)
  } finally {
    logLoading.value = false
  }
}

function showLogs(s: ServiceRow) {
  logOf.value = s
  logPath.value = s.logs[0] ?? ''
  logText.value = ''
  loadLog()
}

const baseName = (p: string) => p.split(/[\\/]/).pop() ?? p

// ---- who uses a port ----

const portInput = ref('')
const portResult = ref<PortRow | null>(null)
const portError = ref('')

async function lookup() {
  portResult.value = null
  portError.value = ''
  const n = Number(portInput.value)
  if (!/^\d+$/.test(portInput.value.trim()) || n < 1 || n > 65535) {
    portError.value = '请输入 1–65535 之间的端口号'
    return
  }
  try {
    portResult.value = await api.portInfo(n)
  } catch (e) {
    portError.value = errText(e)
  }
}

async function kill(p: PortRow) {
  const ok = await confirmDialog({
    title: '结束进程',
    message: `要结束占用端口 ${p.port} 的进程 ${p.owner || '（未知程序）'}（PID ${p.ownerPid}）吗？\n\n进程会被直接终止，没保存的数据会丢失。如果它是一个 Windows 服务，系统可能会把它重新拉起来。`,
    okText: '结束进程',
    danger: true,
  })
  if (!ok) return
  try {
    await api.killPid(p.ownerPid)
    toast('ok', `已结束 PID ${p.ownerPid}`)
    await lookup()
  } catch (e) {
    fail(e, '结束进程')
  }
}
</script>

<template>
  <div class="page-head">
    <div>
      <h1>服务面板</h1>
      <div class="sub">这里只列出本工具安装的服务。开机自启关掉后，需要用的时候再来这里手动启动。</div>
    </div>
    <span class="spacer" />
    <button class="btn" @click="refreshServices().catch(fail)"><Icon name="refresh" />刷新</button>
  </div>

  <div v-if="!state.services.length" class="card empty">
    <div class="big">还没有服务</div>
    <div>安装 MySQL、Redis、Nginx 这类软件后，它们的服务会出现在这里。</div>
    <button class="btn primary" style="margin-top: 14px" @click="go('software')">去软件库</button>
  </div>

  <div v-else class="card" style="padding: 6px 8px">
    <table class="table">
      <thead>
        <tr>
          <th>服务</th>
          <th>状态</th>
          <th>端口</th>
          <th>开机自启</th>
          <th style="text-align: right">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="s in state.services" :key="s.name">
          <td>
            <strong>{{ s.pkgName }}</strong> <span class="muted">{{ s.version }}</span>
            <div class="faint small mono selectable">{{ s.name }}</div>
          </td>
          <td>
            <div class="row">
              <span v-if="serviceBusy(s.name) || s.state === 'starting' || s.state === 'stopping'" class="spin" />
              <span v-else class="dot" :class="{ ok: s.state === 'running', danger: s.state === 'missing' }" />
              {{ SERVICE_STATES[s.state] ?? s.state }}
            </div>
            <div v-if="s.pid" class="faint small">PID {{ s.pid }}</div>
          </td>
          <td>
            <div v-for="p in s.ports" :key="p.name + p.port" class="row small" style="gap: 6px">
              <span class="dot" :class="{ ok: p.listening && s.state === 'running', warn: p.listening && s.state !== 'running' }" />
              <span class="mono">{{ p.port }}</span>
              <span class="muted">{{ p.name }}</span>
              <span v-if="p.listening && s.state !== 'running'" class="tag warn" :title="`PID ${p.ownerPid}`">被 {{ p.owner || '其他程序' }} 占用</span>
            </div>
            <span v-if="!s.ports.length" class="faint">—</span>
          </td>
          <td>
            <button
              class="switch"
              :class="{ on: s.startType === 'auto' }"
              :disabled="s.state === 'missing'"
              :title="s.startType === 'auto' ? '开机时自动启动，点击改为手动' : '不随开机启动，点击改为自动'"
              role="switch"
              :aria-checked="s.startType === 'auto'"
              @click="setAuto(s)"
            />
          </td>
          <td>
            <div class="row" style="justify-content: flex-end; gap: 6px">
              <button v-if="s.state !== 'running'" class="btn sm primary" :disabled="s.state === 'missing' || serviceBusy(s.name)" @click="act(s, 'start')">
                <Icon name="play" :size="12" />启动
              </button>
              <template v-else>
                <button class="btn sm" :disabled="serviceBusy(s.name)" @click="act(s, 'restart')"><Icon name="refresh" :size="12" />重启</button>
                <button class="btn sm danger" :disabled="serviceBusy(s.name)" @click="act(s, 'stop')"><Icon name="stop" :size="12" />停止</button>
              </template>
              <button class="btn sm ghost" :disabled="!s.logs.length" :title="s.logs.length ? '查看日志' : '这个服务没有日志文件'" @click="showLogs(s)"><Icon name="file" /></button>
              <button class="btn sm ghost" title="打开安装目录" @click="openDir(s.dir)"><Icon name="folder" /></button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="card">
    <h2>端口被谁占了？</h2>
    <div class="muted small" style="margin: 2px 0 12px">服务启动不了，多半是端口被别的程序占用。输入端口号查一下。</div>
    <div class="row wrap">
      <input v-model="portInput" class="input mono" style="width: 130px" placeholder="例如 3306" inputmode="numeric" maxlength="5" @keydown.enter="lookup" />
      <button class="btn" @click="lookup"><Icon name="search" />查询</button>
      <span v-if="portError" style="color: var(--danger)">{{ portError }}</span>
      <template v-if="portResult">
        <template v-if="portResult.listening">
          <span class="tag warn">端口 {{ portResult.port }} 正被占用</span>
          <span class="selectable">{{ portResult.owner || '未知程序' }}（PID {{ portResult.ownerPid }}）</span>
          <button class="btn sm danger" :disabled="portResult.ownerPid <= 4" @click="kill(portResult)">结束这个进程</button>
        </template>
        <span v-else class="tag ok">端口 {{ portResult.port }} 空闲</span>
      </template>
    </div>
  </div>

  <Modal v-if="logOf" :title="logOf.pkgName + ' 日志'" wide @close="logOf = null">
    <div class="row" style="margin-bottom: 10px">
      <select v-model="logPath" class="input" style="flex: 1" @change="loadLog">
        <option v-for="l in logOf.logs" :key="l" :value="l">{{ baseName(l) }}</option>
      </select>
      <button class="btn" :disabled="logLoading" @click="loadLog"><Icon name="refresh" />刷新</button>
    </div>
    <div class="faint small mono selectable" style="margin-bottom: 6px">{{ logPath }}</div>
    <div v-if="logError" class="note warn selectable">读不到这个日志：{{ logError }}（服务还没写过日志时文件不存在）</div>
    <pre v-else ref="logBox" class="logbox">{{ logText || '（日志是空的）' }}</pre>
    <template #foot>
      <span class="muted small" style="margin-right: auto">只显示最后 128 KB</span>
      <button class="btn primary" @click="logOf = null">关闭</button>
    </template>
  </Modal>
</template>
