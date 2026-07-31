<template>
  <div class="space-y-6">
    <!-- Header/Overview -->
    <div class="grid grid-cols-1 md:grid-cols-3 gap-6">
      <div v-for="acc in accounts" :key="acc.id" class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
        <div class="flex items-center justify-between mb-4">
          <div class="flex items-center gap-2">
            <el-avatar :size="32" class="bg-blue-100 text-blue-600 font-bold">{{ acc.supplier_name.charAt(0) }}</el-avatar>
            <div>
              <div class="font-bold text-gray-800">{{ acc.supplier_name }}</div>
              <div class="text-xs text-gray-500 font-mono">{{ acc.supplier_code }}</div>
            </div>
          </div>
          <el-tag :type="acc.status === 'active' ? 'success' : 'danger'">{{ acc.status === 'active' ? '正常' : '冻结' }}</el-tag>
        </div>
        <div class="grid grid-cols-2 gap-4">
          <div>
            <div class="text-xs text-gray-500 mb-1">可用余额</div>
            <div class="text-2xl font-bold font-mono text-gray-900">¥{{ acc.balance }}</div>
          </div>
          <div>
            <div class="text-xs text-gray-500 mb-1">授信额度</div>
            <div class="text-lg font-bold font-mono text-gray-400">¥{{ acc.credit_line }}</div>
          </div>
        </div>
        <div class="mt-4 pt-4 border-t border-gray-50 flex justify-end">
             <el-button type="primary" link size="small" @click="handleRecharge(acc)">申请充值</el-button>
        </div>
      </div>
      
      <!-- Placeholder if no accounts -->
      <div v-if="accounts.length === 0 && !loadingAccounts" class="col-span-3 bg-gray-50 rounded-xl p-8 text-center text-gray-400 border border-dashed border-gray-200">
         暂无合作的供应商资金账户
      </div>
    </div>

    <!-- Transactions Table -->
    <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
      <div class="flex justify-between items-center mb-6">
        <h3 class="font-bold text-gray-900">交易明细</h3>
        <div class="flex gap-2">
             <el-select v-model="filterType" placeholder="全部类型" clearable style="width: 120px">
                <el-option label="充值" value="deposit" />
                <el-option label="支付" value="payment" />
                <el-option label="退款" value="refund" />
             </el-select>
             <el-button type="primary" @click="fetchTransactions" :loading="loadingTrans">刷新</el-button>
        </div>
      </div>

      <el-table :data="transactions" v-loading="loadingTrans" style="width: 100%">
        <el-table-column prop="created_at" label="时间" width="180">
            <template #default="{ row }">
                <span class="font-mono text-xs text-gray-500">{{ formatTime(row.created_at) }}</span>
            </template>
        </el-table-column>
        <el-table-column prop="type" label="类型" width="100">
            <template #default="{ row }">
                <el-tag :type="getTypeTag(row.type)">{{ getTypeText(row.type) }}</el-tag>
            </template>
        </el-table-column>
        <el-table-column prop="amount" label="变动金额" width="150" align="right">
            <template #default="{ row }">
                <span :class="row.amount > 0 ? 'text-green-600' : 'text-red-600'" class="font-bold font-mono">
                    {{ row.amount > 0 ? '+' : '' }}{{ row.amount }}
                </span>
            </template>
        </el-table-column>
        <el-table-column prop="balance_after" label="变动后余额" width="150" align="right">
            <template #default="{ row }">
                <span class="font-mono text-gray-400">¥{{ row.balance_after }}</span>
            </template>
        </el-table-column>
        <el-table-column prop="related_order_no" label="关联单号" width="180">
             <template #default="{ row }">
                <span class="font-mono text-xs">{{ row.related_order_no || '-' }}</span>
            </template>
        </el-table-column>
        <el-table-column prop="memo" label="备注" min-width="200" show-overflow-tooltip />
      </el-table>
      
      <div class="flex justify-end mt-4">
        <el-pagination
            background
            layout="prev, pager, next"
            :total="total"
            v-model:current-page="page"
            :page-size="pageSize"
            @current-change="fetchTransactions"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import request from '@/utils/request'
import { ElMessage } from 'element-plus'

// Accounts
const loadingAccounts = ref(false)
const accounts = ref<any[]>([])

// Transactions
const loadingTrans = ref(false)
const transactions = ref<any[]>([])
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const filterType = ref('')

const fetchAccounts = async () => {
    loadingAccounts.value = true
    try {
        const res = await request.get('/finance/accounts')
        accounts.value = res.data.data || []
    } catch (e) {
        ElMessage.error('获取账户信息失败')
    } finally {
        loadingAccounts.value = false
    }
}

const fetchTransactions = async () => {
    loadingTrans.value = true
    try {
        const res = await request.get('/finance/transactions', {
            params: {
                page: page.value,
                page_size: pageSize.value,
                type: filterType.value
            }
        })
        transactions.value = res.data.data || []
        total.value = res.data.total || 0
    } catch (e) {
        ElMessage.error('获取流水失败')
    } finally {
        loadingTrans.value = false
    }
}

watch(filterType, () => {
    page.value = 1
    fetchTransactions()
})

const formatTime = (iso: string) => {
    if (!iso) return ''
    return new Date(iso).toLocaleString()
}

const getTypeTag = (type: string) => {
    const map: any = { deposit: 'success', payment: 'warning', refund: 'primary' }
    return map[type] || 'info'
}

const getTypeText = (type: string) => {
    const map: any = { deposit: '充值', payment: '消费', refund: '退款', credit_adjust: '授信' }
    return map[type] || type
}

// Recharge Logic
import { ElMessageBox } from 'element-plus'
const handleRecharge = (acc: any) => {
    ElMessageBox.alert(
        `请联系供应商进行线下充值。\n\n供应商：${acc.supplier_name}\n联系人：${acc.supplier_contact || '未知'}\n电话：${acc.supplier_phone || '未知'}`,
        '充值指引',
        {
            confirmButtonText: '知道了'
        }
    )
}

onMounted(() => {
    fetchAccounts()
    fetchTransactions()
})
</script>
