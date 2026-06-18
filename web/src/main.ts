import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'

import App from './App.vue'
import router from './router'
import './styles.css'
import { useI18nStore } from './i18n'

const pinia = createPinia()
const app = createApp(App)

app.use(pinia).use(router).use(ElementPlus)

const i18n = useI18nStore(pinia)
document.documentElement.lang = i18n.locale
i18n.loadConfiguredDefaultLocale()

app.mount('#app')
