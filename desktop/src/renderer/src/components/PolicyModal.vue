<template>
  <div class="policy-modal p-6 h-full flex flex-col">
    <div class="mb-4">
      <el-input
        v-model="searchText"
        prefix-icon="Search"
        placeholder="搜索政策关键词 (如: 免票, 退款)..."
        clearable
      />
    </div>

    <div class="flex-1 overflow-y-auto space-y-6 pr-2">
      <div v-if="loading" class="py-10 text-center text-gray-400">
        <el-icon class="is-loading"><Loading /></el-icon> 加载中...
      </div>

      <div v-else-if="filteredPolicies.length === 0" class="py-10 text-center text-gray-400">
        暂无相关政策
      </div>
      
      <section v-for="(group, key) in groupedPolicies" :key="key" v-show="hasMatches(group)">
        <h3 :class="`text-lg font-bold mb-2 border-b pb-1 ${getCategoryColor(key)}`">{{ getCategoryLabel(key) }}</h3>
        <ul class="space-y-3">
           <li v-for="policy in group" :key="policy.id" v-show="matches(policy)" class="bg-white/5 p-3 rounded border border-white/10">
              <div class="font-bold text-gray-200 mb-1">{{ policy.title }}</div>
              <div class="text-sm text-gray-400 whitespace-pre-wrap">{{ policy.content }}</div>
           </li>
        </ul>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import axios from 'axios' // Use global axios or import
import { Loading } from '@element-plus/icons-vue'

const searchText = ref('')
const policies = ref<any[]>([])
const loading = ref(false)

const groupedPolicies = computed(() => {
    const groups: any = {}
    policies.value.forEach(p => {
        if(!groups[p.category]) groups[p.category] = []
        groups[p.category].push(p)
    })
    return groups
})

const filteredPolicies = computed(() => {
    if(!searchText.value) return policies.value
    return policies.value.filter(p => matches(p))
})

const fetchPolicies = async () => {
    loading.value = true
    try {
        const res = await axios.get('/policies') // Assuming axios is configured with baseURL
        policies.value = res.data.data || []
    } catch (e) {
        console.error(e)
    } finally {
        loading.value = false
    }
}

const matches = (policy: any) => {
    if (!searchText.value) return true
    const keyword = searchText.value.toLowerCase()
    return policy.title.toLowerCase().includes(keyword) || 
           policy.content.toLowerCase().includes(keyword)
}

const hasMatches = (group: any[]) => {
    return group.some(p => matches(p))
}

const getCategoryLabel = (val: string | number) => {
    const map: any = { 'Admission': '入园/免票', 'Discount': '优惠政策', 'Refund': '退改说明', 'Pet': '宠物政策', 'Other': '其他' }
    return map[String(val)] || String(val)
}

const getCategoryColor = (val: string | number) => {
    const map: any = { 'Admission': 'text-blue-400', 'Discount': 'text-green-400', 'Refund': 'text-red-400', 'Pet': 'text-orange-400', 'Other': 'text-gray-400' }
    return map[String(val)] || 'text-gray-400'
}

onMounted(() => {
    fetchPolicies()
})
</script>
