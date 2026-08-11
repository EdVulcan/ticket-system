<template>
  <div class="calculator">
    <div class="calculator-display">
      <div class="expression">{{ expression || ' ' }}</div>
      <div data-testid="calculator-display" class="display-value">{{ displayValue }}</div>
    </div>

    <div class="calculator-keypad">
      <button @click="clear" class="btn-op span-two">清空</button>
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

      <button @click="appendNumber('0')" class="btn-num span-two">0</button>
      <button @click="appendNumber('.')" class="btn-num">.</button>
      <button @click="calculate" class="btn-eq">=</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const props = defineProps<{ active?: boolean }>()

const expression = ref('')
const displayValue = ref('0')
const resetNext = ref(false)
const storedValue = ref<number | null>(null)
const pendingOperator = ref<'+' | '-' | '*' | '/' | null>(null)

const operatorLabel = (operator: string) => operator === '*' ? '×' : operator === '/' ? '÷' : operator

const parseDisplay = () => {
  const value = Number(displayValue.value)
  return Number.isFinite(value) ? value : null
}

const applyOperation = (left: number, right: number, operator: '+' | '-' | '*' | '/') => {
  switch (operator) {
    case '+': return left + right
    case '-': return left - right
    case '*': return left * right
    case '/': return right === 0 ? null : left / right
  }
}

const showResult = (value: number) => {
  displayValue.value = String(Math.round((value + Number.EPSILON) * 100) / 100)
}

const appendNumber = (num: string) => {
  if (resetNext.value || !Number.isFinite(Number(displayValue.value))) {
    displayValue.value = ''
    resetNext.value = false
  }
  if (num === '.' && displayValue.value.includes('.')) return
  if (num === '.' && displayValue.value === '') displayValue.value = '0'
  if (displayValue.value === '0' && num !== '.') {
    displayValue.value = num
  } else {
    displayValue.value += num
  }
}

const appendOperator = (op: '+' | '-' | '*' | '/') => {
  const current = parseDisplay()
  if (current === null) return
  if (pendingOperator.value && storedValue.value !== null && !resetNext.value) {
    const result = applyOperation(storedValue.value, current, pendingOperator.value)
    if (result === null) {
      displayValue.value = '不能除以零'
      expression.value = ''
      storedValue.value = null
      pendingOperator.value = null
      resetNext.value = true
      return
    }
    showResult(result)
    storedValue.value = result
  } else {
    storedValue.value = current
  }
  pendingOperator.value = op
  expression.value = `${displayValue.value} ${operatorLabel(op)}`
  resetNext.value = true
}

const calculate = () => {
  if (!pendingOperator.value || storedValue.value === null || resetNext.value) return
  const current = parseDisplay()
  if (current === null) return
  const result = applyOperation(storedValue.value, current, pendingOperator.value)
  if (result === null) {
    displayValue.value = '不能除以零'
  } else {
    showResult(result)
  }
  expression.value = ''
  storedValue.value = null
  pendingOperator.value = null
  resetNext.value = true
}

const clear = () => {
  displayValue.value = '0'
  expression.value = ''
  resetNext.value = false
  storedValue.value = null
  pendingOperator.value = null
}

const backspace = () => {
  if (resetNext.value) return
  displayValue.value = displayValue.value.slice(0, -1) || '0'
}

// Keyboard Support
const handleKeydown = (e: KeyboardEvent) => {
  if (!props.active) return
  const supported = /^[0-9.+\-*/=]$/.test(e.key) || ['Enter', 'Backspace', 'Escape'].includes(e.key)
  if (!supported) return
  e.preventDefault()
  e.stopPropagation()
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
.calculator { width: 100%; user-select: none; color: #202721; }
.calculator-display { margin-bottom: 12px; padding: 12px 14px; border: 1px solid #cbd3cc; border-radius: 7px; background: #f3f6f3; text-align: right; }
.expression { height: 18px; color: #79837b; font-size: 12px; line-height: 18px; }
.display-value { min-height: 34px; overflow: hidden; color: #1b241e; font-family: Consolas, "SFMono-Regular", monospace; font-size: 27px; font-weight: 700; line-height: 34px; text-overflow: ellipsis; white-space: nowrap; }
.calculator-keypad { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 7px; }
.calculator-keypad button { height: 50px; border: 1px solid #d4dad4; border-radius: 6px; cursor: pointer; font-size: 16px; font-weight: 700; }
.calculator-keypad button:active { transform: translateY(1px); }
.span-two { grid-column: span 2; }
.btn-num { background: #fff; color: #303932; }
.btn-num:hover { border-color: #9eaaa1; background: #f6f8f6; }
.btn-op { border-color: #e3d2b6 !important; background: #fff7e9; color: #9c590d; }
.btn-op:hover { background: #fbeed9; }
.btn-eq { border-color: #14734a !important; background: #14734a; color: #fff; }
.btn-eq:hover { background: #0e5a3a; }
</style>
