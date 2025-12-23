<template>
  <div class="calculator bg-gray-800 p-4 rounded-lg shadow-xl w-64 select-none">
    <!-- Display -->
    <div class="bg-gray-100 rounded mb-4 p-2 text-right">
      <div class="text-xs text-gray-500 h-4">{{ expression }}</div>
      <div class="text-2xl font-mono font-bold text-gray-900 truncate">{{ displayValue }}</div>
    </div>

    <!-- Keypad -->
    <div class="grid grid-cols-4 gap-2">
      <button @click="clear" class="btn-op col-span-2">C</button>
      <button @click="backspace" class="btn-op">←</button>
      <button @click="appendOperator('/')" class="btn-op">÷</button>

      <button @click="appendNumber('7')" class="btn-num">7</button>
      <button @click="appendNumber('8')" class="btn-num">8</button>
      <button @click="appendNumber('9')" class="btn-num">9</button>
      <button @click="appendOperator('*')" class="btn-op">×</button>

      <button @click="appendNumber('4')" class="btn-num">4</button>
      <button @click="appendNumber('5')" class="btn-num">5</button>
      <button @click="appendNumber('6')" class="btn-num">6</button>
      <button @click="appendOperator('-')" class="btn-op">-</button>

      <button @click="appendNumber('1')" class="btn-num">1</button>
      <button @click="appendNumber('2')" class="btn-num">2</button>
      <button @click="appendNumber('3')" class="btn-num">3</button>
      <button @click="appendOperator('+')" class="btn-op">+</button>

      <button @click="appendNumber('0')" class="btn-num col-span-2">0</button>
      <button @click="appendNumber('.')" class="btn-num">.</button>
      <button @click="calculate" class="btn-eq bg-blue-600 text-white border-blue-700">=</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const expression = ref('')
const displayValue = ref('0')
const resetNext = ref(false)

const appendNumber = (num: string) => {
  if (resetNext.value) {
    displayValue.value = ''
    resetNext.value = false
  }
  if (displayValue.value === '0' && num !== '.') {
    displayValue.value = num
  } else {
    displayValue.value += num
  }
}

const appendOperator = (op: string) => {
  calculate()
  expression.value = displayValue.value + ' ' + op
  resetNext.value = true
}

const calculate = () => {
  if (!expression.value || resetNext.value) return

  try {
    // Safe eval alternative or simple parsing
    // For simple calc, eval is acceptable in client-side blocked env if clearly limited,
    // but better to use Function
    const fullExp = expression.value + ' ' + displayValue.value
    // Replace visual operators
    const evalExp = fullExp.replace(/×/g, '*').replace(/÷/g, '/')
    
    // Simple evaluation
    const result = new Function('return ' + evalExp)()
    
    displayValue.value = String(Number(result.toFixed(2))) // Fix floating point
    expression.value = ''
    resetNext.value = true
  } catch (e) {
    displayValue.value = 'Error'
    resetNext.value = true
  }
}

const clear = () => {
  displayValue.value = '0'
  expression.value = ''
  resetNext.value = false
}

const backspace = () => {
  if (resetNext.value) return
  displayValue.value = displayValue.value.slice(0, -1) || '0'
}

// Keyboard Support
const handleKeydown = (e: KeyboardEvent) => {
  if (e.key >= '0' && e.key <= '9') appendNumber(e.key)
  else if (e.key === '.') appendNumber('.')
  else if (e.key === '+') appendOperator('+')
  else if (e.key === '-') appendOperator('-')
  else if (e.key === '*') appendOperator('*')
  else if (e.key === '/') appendOperator('/')
  else if (e.key === 'Enter' || e.key === '=') calculate()
  else if (e.key === 'Backspace') backspace()
  else if (e.key === 'Escape') clear()
}

onMounted(() => window.addEventListener('keydown', handleKeydown))
onUnmounted(() => window.removeEventListener('keydown', handleKeydown))
</script>

<style scoped>
.btn-num {
  @apply bg-gray-600 text-white font-bold h-12 rounded hover:bg-gray-500 active:bg-gray-700 transition-colors shadow;
}
.btn-op {
  @apply bg-orange-500 text-white font-bold h-12 rounded hover:bg-orange-400 active:bg-orange-600 transition-colors shadow;
}
.btn-eq {
  @apply h-12 rounded hover:bg-blue-500 active:bg-blue-700 transition-colors shadow;
}
</style>
