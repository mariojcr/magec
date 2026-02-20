<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <h2 class="text-sm font-semibold text-arena-200">Skills</h2>
      <button @click="openDialog()" class="px-3 py-1.5 bg-sol-500 hover:bg-sol-600 text-piedra-950 text-xs font-medium rounded-lg transition-colors">
        + New Skill
      </button>
    </div>

    <SkeletonCard v-if="store.loading && !store.skills.length" />

    <EmptyState v-else-if="!store.skills.length" title="No skills configured" subtitle="Create reusable skills that can be assigned to agents" icon="skill" color="cyan" actionLabel="+ New Skill" @action="openDialog()" />

    <div v-else class="grid gap-3 grid-cols-1 sm:grid-cols-2">
      <Card v-for="sk in store.skills" :key="sk.id" color="cyan">
        <div class="flex items-start justify-between gap-3 mb-2">
          <div class="flex items-center gap-3 min-w-0">
            <div class="w-8 h-8 rounded-lg flex items-center justify-center flex-shrink-0 bg-cyan-500/15">
              <Icon name="skill" size="md" class="text-cyan-400" />
            </div>
            <div class="min-w-0">
              <h3 class="font-medium text-arena-100 text-sm">{{ sk.name }}</h3>
              <p v-if="sk.description" class="text-[10px] text-arena-500 truncate">{{ sk.description }}</p>
            </div>
          </div>
          <div class="flex gap-0.5 flex-shrink-0">
            <button @click="openDialog(sk)" class="p-1.5 hover:bg-piedra-800 rounded-lg" title="Edit">
              <Icon name="edit" size="sm" class="text-arena-400" />
            </button>
            <button @click="handleDelete(sk)" class="p-1.5 hover:bg-piedra-800 rounded-lg" title="Delete">
              <Icon name="trash" size="sm" class="text-arena-400 hover:text-lava-400" />
            </button>
          </div>
        </div>
        <p class="text-[10px] text-arena-500 line-clamp-2 italic">"{{ sk.instructions }}"</p>
        <div v-if="sk.references && sk.references.length" class="flex flex-wrap gap-1 mt-2">
          <Badge variant="muted" v-for="ref in sk.references" :key="ref.filename">
            <span class="text-arena-500 mr-0.5">📎</span> {{ ref.filename }}
          </Badge>
        </div>
        <div v-if="usedBy(sk.id).length" class="flex flex-wrap gap-1 mt-2">
          <Tooltip v-for="ref in usedBy(sk.id)" :key="ref.name" :text="ref.tooltip">
            <Badge variant="muted">{{ ref.name }}</Badge>
          </Tooltip>
        </div>
        <p v-else class="text-[10px] text-arena-600 mt-2">Not linked to any agent</p>
      </Card>
    </div>

    <SkillDialog ref="dialog" @saved="store.refresh()" />
  </div>
</template>

<script setup>
import { inject, ref, onMounted, onUnmounted } from 'vue'
import { useDataStore } from '../../lib/stores/data.js'
import { skillsApi } from '../../lib/api/index.js'
import Card from '../../components/Card.vue'
import Badge from '../../components/Badge.vue'
import Tooltip from '../../components/Tooltip.vue'
import Icon from '../../components/Icon.vue'
import EmptyState from '../../components/EmptyState.vue'
import SkeletonCard from '../../components/SkeletonCard.vue'
import SkillDialog from './SkillDialog.vue'

const store = useDataStore()
const dialog = ref(null)
const requestDelete = inject('requestDelete')
const toast = inject('toast')
const registerNew = inject('registerNew')
onMounted(() => registerNew(() => openDialog()))
onUnmounted(() => registerNew(null))

function openDialog(skill = null) {
  dialog.value?.open(skill)
}

function usedBy(id) {
  const refs = []
  for (const a of store.agents) {
    if ((a.skills || []).includes(id)) {
      const name = a.name || a.id
      const prompt = a.systemPrompt ? a.systemPrompt.slice(0, 80) + (a.systemPrompt.length > 80 ? '...' : '') : ''
      refs.push({ name, tooltip: a.description || prompt })
    }
  }
  return refs
}

function handleDelete(sk) {
  requestDelete(`Delete skill "${sk.name}"? This cannot be undone.`, async () => {
    try {
      await skillsApi.delete(sk.id)
      await store.refresh()
    } catch (e) {
      toast.error(e.message)
    }
  })
}
</script>
