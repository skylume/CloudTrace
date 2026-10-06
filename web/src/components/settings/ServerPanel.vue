<script setup lang="ts">
/**
 * 服务面板：面板从哪儿访问、门锁是什么、怎么让它重来。
 *
 * 这三件事放在一起是因为它们互相牵制：开了局域网访问就必须有密码，改了监听
 * 地址要重启才生效。分开摆的话，用户会在三个地方之间来回找「为什么开不了」。
 *
 * 地址一律由后端枚举下发（本机地址 + 按网卡算出的局域网地址）：前端拿不到
 * 本机网卡，而「手机该输什么」正是用户开了那个开关之后最想知道的事。
 */
import { computed, ref } from 'vue'

import { t } from '@/i18n'
import { useSystemStore } from '@/stores/system'
import { useUIStore } from '@/stores/ui'

const system = useSystemStore()
const ui = useUIStore()

const password = ref('')
const editing = ref(false)

const server = computed(() => system.server)

/** 局域网地址；没有就说明只绑了回环。 */
const lanURLs = computed(() => server.value?.lan_urls ?? [])

const passwordLabel = computed(() => {
  if (!system.auth.password_set) return t('server.password.none')
  return system.auth.legacy ? t('server.password.legacy') : t('server.password.set')
})

/** 密码至少 8 位——与服务端的下限一致，前端比后端松才是 bug。 */
const canSubmit = computed(() => password.value.trim().length >= 8)

function submitPassword(): void {
  if (!canSubmit.value) return
  if (!system.setPassword(password.value.trim())) {
    ui.pushToast({ kind: 'bad', message: t('common.loading') })
    return
  }
  password.value = ''
  editing.value = false
  ui.pushToast({ kind: 'ok', message: t('server.password.saved') })
}

function restart(): void {
  if (!system.restart()) {
    ui.pushToast({ kind: 'bad', message: t('server.restartFailed') })
  }
}
</script>

<template>
  <section class="ct-card">
    <h2 class="ct-card-title">{{ t('server.title') }}</h2>

    <dl class="info">
      <dt>{{ t('server.local') }}</dt>
      <dd>
        <a v-if="server" class="ct-link" :href="server.local_url" target="_blank" rel="noreferrer">
          {{ server.local_url }}
        </a>
        <span v-else class="ct-subtle">—</span>
      </dd>

      <dt>{{ t('server.lan') }}</dt>
      <dd>
        <template v-if="lanURLs.length > 0">
          <a
            v-for="url in lanURLs"
            :key="url"
            class="ct-link lan"
            :href="url"
            target="_blank"
            rel="noreferrer"
          >
            {{ url }}
          </a>
        </template>
        <span v-else class="ct-subtle">{{ t('server.lan.off') }}</span>
      </dd>

      <dt>{{ t('server.version') }}</dt>
      <dd class="ct-mono">{{ server?.version ?? '—' }}</dd>

      <dt>{{ t('server.dataDir') }}</dt>
      <dd class="ct-mono wrap">{{ server?.data_dir ?? '—' }}</dd>
    </dl>

    <!--
      访问密码。设过之后不再显示它本身——服务端存的是加盐哈希，拿不回来，
      界面上假装能回填只会让人以为「忘了也没关系」。
    -->
    <div class="row">
      <div class="row-info">
        <span>{{ t('server.password') }}</span>
        <span class="ct-subtle">{{ passwordLabel }}</span>
      </div>
      <template v-if="editing">
        <input
          v-model="password"
          class="ct-input pw"
          type="password"
          autocomplete="new-password"
          :placeholder="t('server.password.placeholder')"
          @keyup.enter="submitPassword"
        />
        <button type="button" class="ct-btn ct-btn--primary" :disabled="!canSubmit" @click="submitPassword">
          {{ t('common.save') }}
        </button>
        <button type="button" class="ct-btn" @click="editing = false">{{ t('common.cancel') }}</button>
      </template>
      <button v-else type="button" class="ct-btn" @click="editing = true">
        {{ system.auth.password_set ? t('server.password.change') : t('server.password.setup') }}
      </button>
    </div>
    <p v-if="editing" class="ct-subtle note">{{ t('server.password.hint') }}</p>

    <!--
      重启。放在这里而不是危险区：它本身不破坏任何东西，只是把改过的配置真正
      用起来——而「改完要重启」的提示就出现在这一页。
    -->
    <div class="row">
      <div class="row-info">
        <span>{{ t('server.restart') }}</span>
        <span class="ct-subtle">{{ t('server.restartHint') }}</span>
      </div>
      <button type="button" class="ct-btn" :disabled="system.restarting" @click="restart">
        {{ system.restarting ? t('settings.restarting') : t('server.restartAction') }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.info {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--space-1) var(--space-4);
  margin: 0 0 var(--space-3);
  font-size: var(--font-size-sm);
}

.info dt {
  color: var(--color-text-subtle);
  white-space: nowrap;
}

.info dd {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
}

/* 地址要能一眼抄走，所以用等宽并允许折行，而不是被省略号截掉。 */
.info dd a {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}

.wrap {
  overflow-wrap: anywhere;
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-3);
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border);
}

.row-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 180px;
  flex: 1;
  font-size: var(--font-size-sm);
}

.pw {
  flex: 1 1 200px;
}

.note {
  margin: var(--space-2) 0 0;
  font-size: var(--font-size-xs);
}
</style>
