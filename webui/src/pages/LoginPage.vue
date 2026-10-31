<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { login, isSourceKey } from '@/api/sources'
import { useAppStore } from '@/stores/app'
import { errMessage } from '@/api/http'

const route = useRoute()
const router = useRouter()
const app = useAppStore()

const srcParam = route.params.src as string
const src = isSourceKey(srcParam) ? srcParam : 'picacg'
const sourceName = computed(
  () => app.sources.find((s) => s.key === src)?.name ?? (src === 'jm' ? '禁漫天堂' : 'PicACG'),
)

const username = ref('')
const password = ref('')
const submitting = ref(false)
const error = ref('')
const succeeded = ref(false)

async function submit(): Promise<void> {
  if (submitting.value) return
  if (!username.value.trim() || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  submitting.value = true
  error.value = ''
  try {
    await login(src, username.value.trim(), password.value)
    succeeded.value = true
    await app.loadSources()
    setTimeout(() => {
      void router.replace({ name: 'explore' })
    }, 600)
  } catch (e) {
    error.value = errMessage(e)
  } finally {
    submitting.value = false
  }
}

onMounted(() => {
  void app.loadSources()
})
</script>

<template>
  <div class="page login-page">
    <div class="card">
      <h1>登录 {{ sourceName }}</h1>
      <p class="hint">凭据将保存在网关侧，仅用于访问 {{ sourceName }} 的个人内容。</p>

      <form @submit.prevent="submit">
        <label class="field">
          <span>用户名</span>
          <input
            v-model="username"
            type="text"
            autocomplete="username"
            placeholder="用户名"
            data-testid="login-username"
          />
        </label>
        <label class="field">
          <span>密码</span>
          <input
            v-model="password"
            type="password"
            autocomplete="current-password"
            placeholder="密码"
            data-testid="login-password"
          />
        </label>

        <p v-if="error" class="error" data-testid="login-error">{{ error }}</p>
        <p v-else-if="succeeded" class="success" data-testid="login-success">登录成功，正在返回…</p>

        <button
          type="submit"
          class="btn primary submit"
          :disabled="submitting || succeeded"
          data-testid="login-submit"
        >
          <span v-if="submitting" class="spinner tiny" />
          {{ submitting ? '登录中…' : '登录' }}
        </button>
      </form>

      <button class="btn back" type="button" @click="router.back()">返回</button>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  display: flex;
  justify-content: center;
  padding-top: 7vh;
}

.card {
  width: min(400px, 100%);
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: 16px;
  padding: 26px 24px;
  box-shadow: var(--shadow);
}

h1 {
  margin: 0 0 6px;
  font-size: 20px;
}

.hint {
  margin: 0 0 20px;
  color: var(--text-faint);
  font-size: 13px;
}

.field {
  display: block;
  margin-bottom: 14px;
}

.field span {
  display: block;
  font-size: 13px;
  color: var(--text-dim);
  margin-bottom: 6px;
}

.field input {
  width: 100%;
  padding: 10px 12px;
  border-radius: 10px;
  border: 1px solid var(--border);
  background: var(--bg);
  color: var(--text);
  font-size: 14.5px;
  outline: none;
}

.field input:focus {
  border-color: var(--accent);
}

.error {
  color: var(--danger);
  font-size: 13.5px;
  margin: 4px 0 12px;
}

.success {
  color: var(--ok);
  font-size: 13.5px;
  margin: 4px 0 12px;
}

.submit {
  width: 100%;
  padding: 11px;
  font-size: 15px;
}

.back {
  width: 100%;
  margin-top: 10px;
}

.spinner.tiny {
  width: 14px;
  height: 14px;
  border-width: 2px;
}
</style>
