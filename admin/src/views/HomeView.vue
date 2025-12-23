<template>
  <div class="space-y-6">
    <div class="bg-white rounded-xl p-8 shadow-sm border border-gray-100 flex flex-col items-center justify-center text-center">
      <h1 class="text-3xl font-bold text-gray-800 mb-2">欢迎使用票务管理系统</h1>
      <p class="text-gray-500 mb-8">Efficient, Smart, and Reliable Ticket Management SaaS.</p>
      
      <div class="grid grid-cols-1 md:grid-cols-3 gap-6 w-full max-w-4xl">
        <div class="p-6 bg-blue-50 rounded-lg border border-blue-100 cursor-pointer hover:shadow-md transition-all" @click="$router.push('/order')">
          <div class="text-blue-600 font-bold text-lg mb-2">订单管理</div>
          <div class="text-gray-600 text-sm">查看与处理所有销售订单</div>
        </div>
        <div class="p-6 bg-green-50 rounded-lg border border-green-100 cursor-pointer hover:shadow-md transition-all" @click="$router.push('/product')">
          <div class="text-green-600 font-bold text-lg mb-2">产品管理</div>
          <div class="text-gray-600 text-sm">配置门票、套票及价格策略</div>
        </div>
        <div class="p-6 bg-purple-50 rounded-lg border border-purple-100 cursor-pointer hover:shadow-md transition-all" @click="$router.push('/distribution')">
          <div class="text-purple-600 font-bold text-lg mb-2">分销中心</div>
          <div class="text-gray-600 text-sm">连接上下游，拓展销售渠道</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
</script>
