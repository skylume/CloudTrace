import { createPinia } from 'pinia'
import { createApp } from 'vue'

import App from './App.vue'
import { bootstrap } from './stores/bridge'

import './styles/tokens.css'
import './styles/themes.css'
import './styles/base.css'

const app = createApp(App)
app.use(createPinia())
app.mount('#app')

// 挂载之后再连后端：首屏先出来，连接状态由界面自己显示，
// 不必让用户盯着一个白屏等 WebSocket 握手。
bootstrap()
