<template>
  <div class="space-y-6">
    <!-- Header Section -->
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">控制台</h1>
        <p class="text-sm text-gray-500 mt-1">欢迎回来，今日运营数据如下。</p>
      </div>
      <el-button type="primary" class="!rounded-lg !px-6 shadow-lg shadow-indigo-500/30">
        <el-icon class="mr-2"><Download /></el-icon> 导出报表
      </el-button>
    </div>

    <!-- Stats Cards -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
      <div v-for="(stat, index) in stats" :key="index" class="bg-white rounded-xl p-6 shadow-sm border border-gray-100 hover:shadow-md transition-shadow">
        <div class="flex items-center justify-between mb-4">
          <div class="w-12 h-12 rounded-lg flex items-center justify-center" :class="stat.bgClass">
            <el-icon :size="24" :class="stat.textClass"><component :is="stat.icon" /></el-icon>
          </div>
          <span class="text-sm font-medium" :class="stat.trend > 0 ? 'text-green-600' : 'text-red-600'">
            {{ stat.trend > 0 ? '+' : '' }}{{ stat.trend }}%
          </span>
        </div>
        <div class="text-3xl font-bold text-gray-900 mb-1">{{ stat.value }}</div>
        <div class="text-sm text-gray-500">{{ stat.label }}</div>
      </div>
    </div>

    <!-- Charts Section (Placeholder) -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
      <!-- Main Chart -->
      <div class="lg:col-span-2 bg-white rounded-xl p-6 shadow-sm border border-gray-100">
        <div class="flex items-center justify-between mb-6">
          <h3 class="text-lg font-bold text-gray-900">营收概览</h3>
          <el-radio-group v-model="period" size="small">
            <el-radio-button value="Day">今日</el-radio-button>
            <el-radio-button value="Week">本周</el-radio-button>
            <el-radio-button value="Month">本月</el-radio-button>
          </el-radio-group>
        </div>
        <div class="h-64 bg-gray-50 rounded-lg flex items-center justify-center text-gray-400 border border-dashed border-gray-200">
          图表占位符 (ECharts/Chart.js)
        </div>
      </div>

      <!-- Recent Activity -->
      <div class="bg-white rounded-xl p-6 shadow-sm border border-gray-100">
        <h3 class="text-lg font-bold text-gray-900 mb-6">最近动态</h3>
        <div class="space-y-6">
          <div v-for="i in 4" :key="i" class="flex items-start gap-4">
            <div class="w-8 h-8 rounded-full bg-indigo-50 flex-shrink-0 flex items-center justify-center text-indigo-600 font-bold text-xs">
              {{ i }}
            </div>
            <div>
              <p class="text-sm font-medium text-gray-900">新订单 #20240{{ i }}</p>
              <p class="text-xs text-gray-500 mt-1">2 分钟前</p>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const period = ref('Week')

const stats = [
  { label: '总销售额', value: '¥124,592', trend: 12.5, icon: 'Money', bgClass: 'bg-indigo-50', textClass: 'text-indigo-600' },
  { label: '总订单量', value: '8,549', trend: 8.2, icon: 'Tickets', bgClass: 'bg-blue-50', textClass: 'text-blue-600' },
  { label: '今日核销', value: '24', trend: -2.4, icon: 'CircleCheck', bgClass: 'bg-orange-50', textClass: 'text-orange-600' },
  { label: '在线设备', value: '142', trend: 4.6, icon: 'Monitor', bgClass: 'bg-green-50', textClass: 'text-green-600' },
]
</script>
