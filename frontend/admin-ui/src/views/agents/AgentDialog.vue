<template>
  <AppDialog ref="dialogRef" :title="isEdit ? 'Edit Agent' : 'New Agent'" size="lg" @save="save">
    <div class="space-y-4">
      <div>
        <FormLabel label="Name" :required="true" />
        <FormInput v-model="form.name" placeholder="My Agent" :required="true" />
      </div>
      <div>
        <FormLabel label="Description" />
        <FormInput v-model="form.description" placeholder="What this agent does..." />
      </div>
      <div>
        <FormLabel label="Tags" />
        <div class="flex flex-wrap gap-1.5 mb-2" v-if="form.tags.length">
          <span
            v-for="(tag, i) in form.tags" :key="i"
            class="inline-flex items-center gap-1 px-2 py-0.5 text-[11px] font-medium rounded-lg bg-sol-500/10 text-sol-300 border border-sol-500/20"
          >
            {{ tag }}
            <button type="button" @click="removeTag(i)" class="hover:text-lava-400 transition-colors cursor-pointer">&times;</button>
          </span>
        </div>
        <FormInput v-model="tagInput" placeholder="Type a tag and press Enter" @keydown.enter.prevent="addTag" />
      </div>
      <!-- System Prompt -->
      <details class="group border border-piedra-700/40 rounded-xl">
        <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none text-xs font-medium text-arena-400 hover:text-arena-300">
          <span>System Prompt</span>
          <Icon name="chevronDown" size="md" class="text-arena-500 transition-transform group-open:rotate-180" />
        </summary>
        <div class="px-4 pb-4 space-y-3">
          <textarea v-model="form.systemPrompt" rows="3" class="w-full bg-piedra-800 border border-piedra-700 rounded-lg px-3 py-2 text-sm focus:ring-1 focus:ring-sol-500 focus:border-sol-500 outline-none resize-y" placeholder="Custom system prompt..." />
          <div>
            <FormLabel label="Output Key (optional)" />
            <FormInput v-model="form.outputKey" placeholder="e.g. analysis_result" />
            <p class="text-[10px] text-arena-500 mt-1">Saves this agent's final output under the given key. Other agents can reference it with <code class="text-arena-300 bg-piedra-800 px-0.5 rounded">{key_name}</code> in their system prompt.</p>
          </div>
        </div>
      </details>

      <!-- LLM -->
      <details class="group border border-piedra-700/40 rounded-xl">
        <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none text-xs font-medium text-arena-400 hover:text-arena-300">
          <span>LLM</span>
          <Icon name="chevronDown" size="md" class="text-arena-500 transition-transform group-open:rotate-180" />
        </summary>
        <div class="px-4 pb-4 space-y-4">
          <div class="grid grid-cols-2 gap-3">
            <div>
              <FormLabel label="Backend" />
              <FormSelect v-model="form.llmBackend">
                <option v-for="b in store.backends" :key="b.id" :value="b.id">{{ b.name }} ({{ b.type }})</option>
              </FormSelect>
            </div>
            <div>
              <FormLabel label="Model" />
              <FormInput v-model="form.llmModel" placeholder="qwen3:8b" />
            </div>
          </div>

          <!-- Context Guard -->
          <div class="border-t border-piedra-700/30 pt-3">
            <div class="flex items-center justify-between">
              <div>
                <span class="text-xs font-medium text-arena-400">Context Guard <span class="ml-1 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wider rounded bg-sol-500/15 text-sol-400 border border-sol-500/20">Experimental</span></span>
                <p class="text-[10px] text-arena-500 mt-0.5">Automatically summarize history to prevent context overflow</p>
              </div>
              <FormToggle v-model="form.contextGuardEnabled" />
            </div>

            <div v-if="form.contextGuardEnabled" class="mt-3 grid grid-cols-2 gap-3">
              <div>
                <FormLabel label="Strategy" />
                <FormSelect v-model="form.contextGuardStrategy">
                  <option value="threshold">Token threshold</option>
                  <option value="sliding_window">Sliding window</option>
                </FormSelect>
                <p class="text-[10px] text-arena-500 mt-1" v-if="form.contextGuardStrategy === 'threshold'">Summarizes when token usage approaches the model's context window limit</p>
                <p class="text-[10px] text-arena-500 mt-1" v-else>Summarizes when conversation exceeds a fixed number of messages</p>
              </div>
              <div v-if="form.contextGuardStrategy === 'sliding_window'">
                <FormLabel label="Max turns" />
                <FormInput v-model="form.contextGuardMaxTurns" type="number" placeholder="20" />
                <p class="text-[10px] text-arena-500 mt-1">Number of messages to keep before summarizing older ones</p>
              </div>
              <div v-if="form.contextGuardStrategy === 'threshold'">
                <FormLabel label="Max tokens" />
                <FormInput v-model="form.contextGuardMaxTokens" type="number" placeholder="Auto (model limit)" />
                <p class="text-[10px] text-arena-500 mt-1">Token limit for triggering summarization. Leave empty to auto-detect from model.</p>
              </div>
            </div>
          </div>
        </div>
      </details>

      <!-- MCPs -->
      <details class="group border border-piedra-700/40 rounded-xl">
        <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none text-xs font-medium text-arena-400 hover:text-arena-300">
          <span>MCP Servers</span>
          <Icon name="chevronDown" size="md" class="text-arena-500 transition-transform group-open:rotate-180" />
        </summary>
        <div class="px-4 pb-4">
          <div v-if="store.mcps.length" class="flex flex-wrap gap-1.5">
            <button
              v-for="m in store.mcps" :key="m.id"
              type="button"
              @click="toggleMcp(m.id)"
              class="px-2.5 py-1 text-[11px] font-medium rounded-lg border transition-all cursor-pointer"
              :class="form.mcpServers.includes(m.id)
                ? 'bg-atlantico-500/15 text-atlantico-300 border-atlantico-500/30'
                : 'bg-piedra-800 text-arena-500 border-piedra-700/40 hover:border-piedra-600 hover:text-arena-300'"
            >
              {{ m.name }}
            </button>
          </div>
          <p v-else class="text-xs text-arena-500">No MCP servers defined yet</p>
        </div>
      </details>

      <!-- Skills -->
      <details class="group border border-piedra-700/40 rounded-xl">
        <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none text-xs font-medium text-arena-400 hover:text-arena-300">
          <span>Skills</span>
          <Icon name="chevronDown" size="md" class="text-arena-500 transition-transform group-open:rotate-180" />
        </summary>
        <div class="px-4 pb-4">
          <div v-if="store.skills.length" class="flex flex-wrap gap-1.5">
            <button
              v-for="sk in store.skills" :key="sk.id"
              type="button"
              @click="toggleSkill(sk.id)"
              class="px-2.5 py-1 text-[11px] font-medium rounded-lg border transition-all cursor-pointer"
              :class="form.skills.includes(sk.id)
                ? 'bg-cyan-500/15 text-cyan-300 border-cyan-500/30'
                : 'bg-piedra-800 text-arena-500 border-piedra-700/40 hover:border-piedra-600 hover:text-arena-300'"
            >
              {{ sk.name }}
            </button>
          </div>
          <p v-else class="text-xs text-arena-500">No skills defined yet</p>
        </div>
      </details>

      <!-- A2A -->
      <div class="border border-piedra-700/40 rounded-xl px-4 py-3">
        <div class="flex items-center justify-between">
          <div>
            <span class="text-xs font-medium text-arena-400">A2A Protocol</span>
            <p class="text-[10px] text-arena-500 mt-0.5">Expose this agent via the Agent-to-Agent protocol for external discovery and invocation</p>
          </div>
          <FormToggle v-model="form.a2aEnabled" />
        </div>
      </div>

      <!-- Voice -->
      <details class="group border border-piedra-700/40 rounded-xl">
        <summary class="flex items-center justify-between px-4 py-3 cursor-pointer select-none text-xs font-medium text-arena-400 hover:text-arena-300">
          <span>Voice (STT / TTS)</span>
          <Icon name="chevronDown" size="md" class="text-arena-500 transition-transform group-open:rotate-180" />
        </summary>
        <div class="px-4 pb-4 space-y-4">
          <div class="space-y-3">
            <h4 class="text-[10px] font-medium text-arena-500 uppercase tracking-wider">Transcription (STT)</h4>
            <div class="grid grid-cols-2 gap-3">
              <div>
                <FormLabel label="Backend" />
                <FormSelect v-model="form.transcriptionBackend">
                  <option value="">(none)</option>
                  <option v-for="b in store.backends" :key="b.id" :value="b.id">{{ b.name }} ({{ b.type }})</option>
                </FormSelect>
              </div>
              <div>
                <FormLabel label="Model" />
                <FormInput v-model="form.transcriptionModel" placeholder="whisper-1" />
              </div>
            </div>
          </div>
          <hr class="border-piedra-700/30" />
          <div class="space-y-3">
            <h4 class="text-[10px] font-medium text-arena-500 uppercase tracking-wider">Text-to-Speech (TTS)</h4>
            <div class="grid grid-cols-2 gap-3">
              <div>
                <FormLabel label="Backend" />
                <FormSelect v-model="form.ttsBackend">
                  <option value="">(none)</option>
                  <option v-for="b in store.backends" :key="b.id" :value="b.id">{{ b.name }} ({{ b.type }})</option>
                </FormSelect>
              </div>
              <div>
                <FormLabel label="Model" />
                <FormInput v-model="form.ttsModel" placeholder="tts-1" />
              </div>
            </div>
            <div class="grid grid-cols-2 gap-3">
              <div>
                <FormLabel label="Voice" />
                <FormInput v-model="form.ttsVoice" placeholder="alloy" />
              </div>
              <div>
                <FormLabel label="Speed" />
                <FormInput v-model="form.ttsSpeed" type="number" placeholder="1.0" />
              </div>
            </div>
          </div>
        </div>
      </details>
    </div>
  </AppDialog>
</template>

<script setup>
import { ref, reactive, inject } from 'vue'
import { useDataStore } from '../../lib/stores/data.js'
import { agentsApi } from '../../lib/api/index.js'
import AppDialog from '../../components/AppDialog.vue'
import FormInput from '../../components/FormInput.vue'
import FormSelect from '../../components/FormSelect.vue'
import FormLabel from '../../components/FormLabel.vue'
import FormToggle from '../../components/FormToggle.vue'
import Icon from '../../components/Icon.vue'

const emit = defineEmits(['saved'])
const toast = inject('toast')
const store = useDataStore()
const dialogRef = ref(null)
const editId = ref(null)
const isEdit = ref(false)
const tagInput = ref('')

const form = reactive({
  name: '',
  description: '',
  outputKey: '',
  systemPrompt: '',
  llmBackend: '',
  llmModel: '',
  mcpServers: [],
  skills: [],
  tags: [],
  transcriptionBackend: '',
  transcriptionModel: '',
  ttsBackend: '',
  ttsModel: '',
  ttsVoice: '',
  ttsSpeed: '',
  contextGuardEnabled: false,
  contextGuardStrategy: 'threshold',
  contextGuardMaxTurns: '',
  contextGuardMaxTokens: '',
  a2aEnabled: false,
})

function toggleMcp(id) {
  const idx = form.mcpServers.indexOf(id)
  if (idx === -1) form.mcpServers.push(id)
  else form.mcpServers.splice(idx, 1)
}

function toggleSkill(id) {
  const idx = form.skills.indexOf(id)
  if (idx === -1) form.skills.push(id)
  else form.skills.splice(idx, 1)
}

function addTag() {
  const tag = tagInput.value.trim().toLowerCase()
  if (tag && !form.tags.includes(tag)) {
    form.tags.push(tag)
  }
  tagInput.value = ''
}

function removeTag(i) {
  form.tags.splice(i, 1)
}

function open(agent = null) {
  isEdit.value = !!agent
  editId.value = agent?.id || null
  form.name = agent?.name || ''
  form.description = agent?.description || ''
  form.outputKey = agent?.outputKey || ''
  form.systemPrompt = agent?.systemPrompt || ''
  form.llmBackend = agent?.llm?.backend || ''
  form.llmModel = agent?.llm?.model || ''
  form.mcpServers = [...(agent?.mcpServers || [])]
  form.skills = [...(agent?.skills || [])]
  form.tags = [...(agent?.tags || [])]
  form.transcriptionBackend = agent?.transcription?.backend || ''
  form.transcriptionModel = agent?.transcription?.model || ''
  form.ttsBackend = agent?.tts?.backend || ''
  form.ttsModel = agent?.tts?.model || ''
  form.ttsVoice = agent?.tts?.voice || ''
  form.ttsSpeed = agent?.tts?.speed || ''
  form.contextGuardEnabled = agent?.contextGuard?.enabled || false
  form.contextGuardStrategy = agent?.contextGuard?.strategy || 'threshold'
  form.contextGuardMaxTurns = agent?.contextGuard?.maxTurns || ''
  form.contextGuardMaxTokens = agent?.contextGuard?.maxTokens || ''
  form.a2aEnabled = agent?.a2a?.enabled || false
  dialogRef.value?.open()
}

async function save() {
  const data = {
    name: form.name.trim(),
    description: form.description.trim(),
    outputKey: form.outputKey.trim(),
    systemPrompt: form.systemPrompt.trim(),
    llm: { backend: form.llmBackend, model: form.llmModel.trim() },
    transcription: { backend: form.transcriptionBackend, model: form.transcriptionModel.trim() },
    tts: {
      backend: form.ttsBackend,
      model: form.ttsModel.trim(),
      voice: form.ttsVoice.trim(),
      speed: parseFloat(form.ttsSpeed) || 0,
    },
    mcpServers: form.mcpServers,
    skills: form.skills,
    tags: form.tags.length ? form.tags : undefined,
    contextGuard: form.contextGuardEnabled ? {
      enabled: true,
      strategy: form.contextGuardStrategy,
      maxTurns: parseInt(form.contextGuardMaxTurns) || 0,
      maxTokens: parseInt(form.contextGuardMaxTokens) || 0,
    } : undefined,
    a2a: form.a2aEnabled ? { enabled: true } : undefined,
  }
  try {
    if (isEdit.value) {
      await agentsApi.update(editId.value, data)
    } else {
      await agentsApi.create(data)
    }
    dialogRef.value?.close()
    emit('saved')
  } catch (e) {
    toast.error(e.message)
  }
}

defineExpose({ open })
</script>
