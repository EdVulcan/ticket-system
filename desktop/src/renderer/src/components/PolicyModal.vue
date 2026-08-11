<template>
  <div class="policy-modal">
    <div class="policy-search">
      <el-input
        v-model="searchText"
        prefix-icon="Search"
        placeholder="搜索政策关键词，例如免票、退款"
        clearable
      />
      <span>{{ filteredPolicies.length }} 条</span>
    </div>

    <div class="policy-content">
      <div v-if="loading" class="policy-empty">
        <el-icon class="is-loading"><Loading /></el-icon> 加载中
      </div>

      <div v-else-if="filteredPolicies.length === 0" class="policy-empty">
        暂无相关政策
      </div>
      
      <section v-for="(group, key) in groupedPolicies" :key="key" v-show="hasMatches(group)" class="policy-group">
        <h3><i :class="getCategoryColor(key)"></i>{{ getCategoryLabel(key) }}<small>{{ group.filter(matches).length }}</small></h3>
        <div class="policy-rows">
          <article v-for="policy in group" :key="policy.id" v-show="matches(policy)">
            <strong>{{ policy.title }}</strong>
            <p>{{ policy.content }}</p>
          </article>
        </div>
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
    const map: any = { 'Admission': 'admission', 'Discount': 'discount', 'Refund': 'refund', 'Pet': 'pet', 'Other': 'other' }
    return map[String(val)] || 'other'
}

onMounted(() => {
    fetchPolicies()
})
</script>

<style scoped>
.policy-modal { height: min(560px, 68vh); display: flex; flex-direction: column; color: #202721; }
.policy-search { flex: 0 0 auto; display: flex; align-items: center; gap: 12px; padding-bottom: 14px; border-bottom: 1px solid #e0e5df; }
.policy-search :deep(.el-input) { flex: 1; }
.policy-search :deep(.el-input__wrapper) { min-height: 43px; box-shadow: 0 0 0 1px #cbd3cb inset; }
.policy-search span { min-width: 44px; color: #7a837b; font-size: 12px; text-align: right; }
.policy-content { min-height: 0; flex: 1; overflow-y: auto; padding: 14px 6px 4px 0; }
.policy-empty { min-height: 220px; display: flex; align-items: center; justify-content: center; gap: 7px; color: #8b948d; }
.policy-group + .policy-group { margin-top: 20px; }
.policy-group h3 { display: flex; align-items: center; gap: 8px; margin: 0 0 7px; color: #39423b; font-size: 13px; line-height: 20px; }
.policy-group h3 i { width: 8px; height: 8px; border-radius: 2px; background: #768078; }
.policy-group h3 i.admission { background: #3f79a8; }
.policy-group h3 i.discount { background: #218053; }
.policy-group h3 i.refund { background: #ba4c47; }
.policy-group h3 i.pet { background: #bd741e; }
.policy-group h3 small { color: #929a94; font-size: 11px; font-weight: 400; }
.policy-rows { overflow: hidden; border: 1px solid #dce2dc; border-radius: 7px; background: #fff; }
.policy-rows article { padding: 12px 14px; }
.policy-rows article + article { border-top: 1px solid #e5e9e4; }
.policy-rows strong { color: #263029; font-size: 14px; }
.policy-rows p { margin: 5px 0 0; color: #626d65; font-size: 13px; line-height: 20px; white-space: pre-wrap; }
</style>
