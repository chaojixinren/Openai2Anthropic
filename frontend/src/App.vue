<script setup>
import { computed, onMounted, ref } from 'vue'
import {
  ArrowRight,
  CircleAlert,
  CircleCheckBig,
  KeyRound,
  Layers3,
  LoaderCircle,
  Network,
  Plus,
  RefreshCcw,
  Server,
  Trash2
} from 'lucide-vue-next'

const defaultConfig = () => ({
  bind: '127.0.0.1:3144',
  accessKey: 'change-me',
  strategy: 'round_robin',
  requestTimeoutSeconds: 180,
  upstreams: [makeUpstream(1)]
})

function makeUpstream(index = 1) {
  return {
    name: `upstream-${index}`,
    baseUrl: '',
    apiKey: '',
    enabled: true
  }
}

const form = ref(defaultConfig())
const modelItems = ref([])
const loading = ref(true)
const saving = ref(false)
const refreshing = ref(false)
const notice = ref('')
const error = ref('')
const restartRequired = ref(false)

const enabledCount = computed(() => form.value.upstreams.filter((item) => item.enabled).length)
const proxyBase = computed(() => `http://${form.value.bind || '127.0.0.1:3144'}`)
const strategyLabel = computed(() =>
  form.value.strategy === 'failover' ? '备用优先' : '轮询分发'
)
const envSnippet = computed(
  () => `ANTHROPIC_BASE_URL=${proxyBase.value}
ANTHROPIC_AUTH_TOKEN=${form.value.accessKey || 'your-new-key'}`
)
const curlSnippet = computed(
  () => `curl ${proxyBase.value}/v1/messages \\
  -H "x-api-key: ${form.value.accessKey || 'your-new-key'}" \\
  -H "content-type: application/json" \\
  -d '{"model":"claude-sonnet-4-20250514","max_tokens":256,"messages":[{"role":"user","content":"hello"}]}'`
)

async function loadConfig() {
  loading.value = true
  error.value = ''

  try {
    const response = await fetch('/api/config')
    if (!response.ok) {
      throw new Error('无法读取后端配置')
    }

    const payload = await response.json()
    if (!payload.upstreams?.length) {
      payload.upstreams = [makeUpstream(1)]
    }
    form.value = payload
  } catch (err) {
    error.value = err.message || '加载配置失败'
    form.value = defaultConfig()
  } finally {
    loading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  notice.value = ''
  error.value = ''
  restartRequired.value = false

  try {
    const response = await fetch('/api/config', {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(form.value)
    })

    const payload = await response.json()
    if (!response.ok) {
      throw new Error(payload.message || '保存失败')
    }

    form.value = payload.config
    restartRequired.value = Boolean(payload.restartRequired)
    notice.value = restartRequired.value ? '配置已保存，监听地址变更需要重启后端。' : '配置已保存。'
  } catch (err) {
    error.value = err.message || '保存失败'
  } finally {
    saving.value = false
  }
}

async function refreshModels() {
  refreshing.value = true
  error.value = ''

  try {
    const response = await fetch('/api/models')
    const payload = await response.json()
    if (!response.ok) {
      throw new Error(payload.message || '拉取模型失败')
    }
    modelItems.value = payload.items || []
  } catch (err) {
    error.value = err.message || '拉取模型失败'
  } finally {
    refreshing.value = false
  }
}

function addUpstream() {
  form.value.upstreams.push(makeUpstream(form.value.upstreams.length + 1))
}

function removeUpstream(index) {
  if (form.value.upstreams.length === 1) {
    form.value.upstreams = [makeUpstream(1)]
    return
  }
  form.value.upstreams.splice(index, 1)
}

onMounted(async () => {
  await loadConfig()
  await refreshModels()
})
</script>

<template>
  <main class="relative overflow-hidden">
    <div class="absolute inset-x-0 top-0 -z-10 h-[32rem] bg-[radial-gradient(circle_at_top,rgba(255,255,255,0.85),transparent_55%)]"></div>
    <div class="mx-auto max-w-7xl px-6 py-10 lg:px-10 lg:py-14">
      <section class="mb-10 flex flex-col gap-8 lg:flex-row lg:items-end lg:justify-between">
        <div class="max-w-3xl">
          <div class="zen-pill mb-5 gap-2">
            <Layers3 class="h-3.5 w-3.5" />
            OpenAI to Anthropic Gateway
          </div>
          <h1 class="font-serif text-4xl leading-tight text-stone-800 sm:text-5xl">
            让 OpenAI 上游，在本地显得像一套
            <span class="text-clay">平静而克制</span>
            的 Anthropic 端点。
          </h1>
          <p class="mt-5 max-w-2xl text-sm leading-7 text-mist sm:text-base">
            只填上游 `base`、`key`、代理监听地址和新的访问密钥。其余的轮询、备用策略、模型探测与持久化，交给这层网关去做。
          </p>
        </div>

        <div class="zen-card max-w-md p-6">
          <div class="flex items-center justify-between">
            <div>
              <p class="text-xs uppercase tracking-[0.24em] text-mist">Proxy Summary</p>
              <p class="mt-2 text-2xl font-medium text-stone-800">{{ strategyLabel }}</p>
            </div>
            <div class="rounded-full bg-white/80 p-3 shadow-float">
              <Network class="h-5 w-5 text-pine" />
            </div>
          </div>
          <div class="mt-6 grid grid-cols-2 gap-4">
            <div class="rounded-2xl bg-white/70 p-4">
              <p class="text-xs uppercase tracking-[0.2em] text-mist">Bind</p>
              <p class="mt-2 text-sm text-ink">{{ form.bind }}</p>
            </div>
            <div class="rounded-2xl bg-white/70 p-4">
              <p class="text-xs uppercase tracking-[0.2em] text-mist">Enabled</p>
              <p class="mt-2 text-sm text-ink">{{ enabledCount }} upstreams</p>
            </div>
          </div>
        </div>
      </section>

      <section v-if="loading" class="zen-card flex items-center gap-3 p-8 text-mist">
        <LoaderCircle class="h-5 w-5 animate-spin" />
        正在读取后端配置...
      </section>

      <section v-else class="grid gap-6 xl:grid-cols-[1.4fr_0.85fr]">
        <div class="space-y-6">
          <article class="zen-card p-7 sm:p-8">
            <div class="mb-8 flex items-start justify-between gap-4">
              <div>
                <p class="text-xs uppercase tracking-[0.24em] text-mist">Service Surface</p>
                <h2 class="mt-3 font-serif text-3xl text-stone-800">本地代理入口</h2>
              </div>
              <div class="rounded-full bg-white/80 p-3 shadow-float">
                <Server class="h-5 w-5 text-pine" />
              </div>
            </div>

            <div class="grid gap-5 md:grid-cols-2">
              <label>
                <span class="zen-label">监听地址</span>
                <input v-model="form.bind" class="zen-input" placeholder="127.0.0.1:3144" />
              </label>

              <label>
                <span class="zen-label">新的访问密钥</span>
                <div class="relative">
                  <KeyRound class="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-mist" />
                  <input
                    v-model="form.accessKey"
                    class="zen-input pl-11"
                    placeholder="your-new-key"
                    type="password"
                  />
                </div>
              </label>

              <label>
                <span class="zen-label">策略</span>
                <select v-model="form.strategy" class="zen-input">
                  <option value="round_robin">轮询 / 均衡分发</option>
                  <option value="failover">备用 / 主备切换</option>
                </select>
              </label>

              <label>
                <span class="zen-label">上游请求超时（秒）</span>
                <input
                  v-model.number="form.requestTimeoutSeconds"
                  class="zen-input"
                  min="30"
                  placeholder="180"
                  type="number"
                />
              </label>
            </div>
          </article>

          <article class="zen-card p-7 sm:p-8">
            <div class="mb-8 flex items-start justify-between gap-4">
              <div>
                <p class="text-xs uppercase tracking-[0.24em] text-mist">Upstream Pool</p>
                <h2 class="mt-3 font-serif text-3xl text-stone-800">OpenAI 上游列表</h2>
              </div>
              <button class="zen-button-soft gap-2" type="button" @click="addUpstream">
                <Plus class="h-4 w-4" />
                添加端点
              </button>
            </div>

            <div class="space-y-5">
              <div
                v-for="(upstream, index) in form.upstreams"
                :key="`${upstream.name}-${index}`"
                class="rounded-[24px] border border-white/60 bg-white/65 p-5 shadow-veil"
              >
                <div class="mb-5 flex items-center justify-between gap-3">
                  <div class="flex items-center gap-3">
                    <div class="rounded-full bg-paper p-2 shadow-sm">
                      <ArrowRight class="h-4 w-4 text-pine" />
                    </div>
                    <p class="text-sm text-ink">上游 {{ index + 1 }}</p>
                  </div>
                  <div class="flex items-center gap-3">
                    <label class="flex items-center gap-2 text-sm text-mist">
                      <input v-model="upstream.enabled" class="h-4 w-4 rounded border-oat text-pine focus:ring-pine/30" type="checkbox" />
                      启用
                    </label>
                    <button class="zen-button-soft p-3" type="button" @click="removeUpstream(index)">
                      <Trash2 class="h-4 w-4" />
                    </button>
                  </div>
                </div>

                <div class="grid gap-4 md:grid-cols-3">
                  <label>
                    <span class="zen-label">名称</span>
                    <input v-model="upstream.name" class="zen-input" placeholder="upstream-1" />
                  </label>
                  <label class="md:col-span-2">
                    <span class="zen-label">OpenAI Base</span>
                    <input v-model="upstream.baseUrl" class="zen-input" placeholder="https://api.openai.com/v1" />
                  </label>
                  <label class="md:col-span-3">
                    <span class="zen-label">OpenAI Key</span>
                    <input v-model="upstream.apiKey" class="zen-input" placeholder="sk-..." type="password" />
                  </label>
                </div>
              </div>
            </div>

            <div class="mt-8 flex flex-wrap items-center gap-4">
              <button class="zen-button gap-2" type="button" @click="saveConfig">
                <LoaderCircle v-if="saving" class="h-4 w-4 animate-spin" />
                <CircleCheckBig v-else class="h-4 w-4" />
                保存配置
              </button>
              <button class="zen-button-soft gap-2" type="button" @click="refreshModels">
                <RefreshCcw :class="['h-4 w-4', refreshing && 'animate-spin']" />
                刷新模型列表
              </button>
            </div>

            <div v-if="notice" class="mt-5 flex items-center gap-3 rounded-2xl bg-white/70 px-4 py-3 text-sm text-pine">
              <CircleCheckBig class="h-4 w-4 shrink-0" />
              {{ notice }}
            </div>
            <div v-if="error" class="mt-5 flex items-center gap-3 rounded-2xl bg-white/70 px-4 py-3 text-sm text-amber-700">
              <CircleAlert class="h-4 w-4 shrink-0" />
              {{ error }}
            </div>
          </article>
        </div>

        <div class="space-y-6">
          <article class="zen-card p-7">
            <p class="text-xs uppercase tracking-[0.24em] text-mist">Quick Start</p>
            <h2 class="mt-3 font-serif text-3xl text-stone-800">客户端接入</h2>

            <div class="mt-6 space-y-4">
              <div class="rounded-[24px] bg-white/70 p-5 shadow-veil">
                <p class="text-xs uppercase tracking-[0.2em] text-mist">Claude Code Env</p>
                <pre class="mt-3 overflow-x-auto whitespace-pre-wrap break-all text-sm leading-7 text-ink">{{ envSnippet }}</pre>
              </div>
              <div class="rounded-[24px] bg-white/70 p-5 shadow-veil">
                <p class="text-xs uppercase tracking-[0.2em] text-mist">Smoke Curl</p>
                <pre class="mt-3 overflow-x-auto whitespace-pre-wrap break-all text-sm leading-7 text-ink">{{ curlSnippet }}</pre>
              </div>
            </div>
          </article>

          <article class="zen-card p-7">
            <div class="flex items-start justify-between gap-4">
              <div>
                <p class="text-xs uppercase tracking-[0.24em] text-mist">Discovered Models</p>
                <h2 class="mt-3 font-serif text-3xl text-stone-800">模型探测</h2>
              </div>
              <button class="zen-button-soft p-3" type="button" @click="refreshModels">
                <RefreshCcw :class="['h-4 w-4', refreshing && 'animate-spin']" />
              </button>
            </div>

            <div class="mt-6 space-y-4">
              <div
                v-for="item in modelItems"
                :key="item.name"
                class="rounded-[24px] bg-white/70 p-5 shadow-veil"
              >
                <div class="flex items-center justify-between gap-4">
                  <div>
                    <p class="text-sm text-ink">{{ item.name }}</p>
                    <p class="mt-1 text-xs text-mist">{{ item.baseUrl }}</p>
                  </div>
                  <span class="zen-pill">{{ item.models?.length || 0 }} models</span>
                </div>
                <p v-if="item.error" class="mt-3 text-sm text-amber-700">{{ item.error }}</p>
                <div v-else class="mt-4 flex flex-wrap gap-2">
                  <span
                    v-for="model in item.models"
                    :key="model"
                    class="rounded-full bg-paper px-3 py-1 text-xs text-mist"
                  >
                    {{ model }}
                  </span>
                </div>
              </div>
            </div>
          </article>

          <article class="zen-card p-7">
            <p class="text-xs uppercase tracking-[0.24em] text-mist">Notes</p>
            <h2 class="mt-3 font-serif text-3xl text-stone-800">行为说明</h2>
            <ul class="mt-5 space-y-3 text-sm leading-7 text-mist">
              <li>轮询模式会把每次请求均匀分发到当前健康的上游。</li>
              <li>备用模式会优先走列表前面的上游，失败后再切到后续端点。</li>
              <li>监听地址变更会写入配置，但后端进程需要重启后才会重新绑定。</li>
            </ul>
          </article>
        </div>
      </section>
    </div>
  </main>
</template>
