/**
 * ESLint 扁平配置。
 *
 * 之前 `package.json` 里有 `"lint": "eslint ."` 和 eslint 依赖，却没有配置文件——
 * 这条命令跑起来只会报「找不到配置」，等于门禁里挂着一条永远失败的命令。
 *
 * 规则取「推荐集 + 几条针对本项目的补充」，不追求把每条可开的规则都打开：
 * 规则多了以后没人会去读告警，不如少而准。
 */
import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  {
    // 产物、依赖、以及生成物都不检查。
    ignores: ['dist/**', 'node_modules/**', 'public/**'],
  },

  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/recommended'],

  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        // 让 .vue 里的 <script setup lang="ts"> 走 TS 解析。
        parser: tseslint.parser,
      },
    },
  },

  {
    languageOptions: {
      globals: {
        // 浏览器环境。不引 globals 包：这里用到的就这些，写出来更清楚。
        window: 'readonly',
        document: 'readonly',
        navigator: 'readonly',
        localStorage: 'readonly',
        sessionStorage: 'readonly',
        fetch: 'readonly',
        WebSocket: 'readonly',
        Notification: 'readonly',
        FileReader: 'readonly',
        FileList: 'readonly',
        File: 'readonly',
        Blob: 'readonly',
        URL: 'readonly',
        URLSearchParams: 'readonly',
        AbortController: 'readonly',
        AbortSignal: 'readonly',
        setTimeout: 'readonly',
        clearTimeout: 'readonly',
        setInterval: 'readonly',
        clearInterval: 'readonly',
        requestAnimationFrame: 'readonly',
        cancelAnimationFrame: 'readonly',
        matchMedia: 'readonly',
        getComputedStyle: 'readonly',
        console: 'readonly',
        performance: 'readonly',
        crypto: 'readonly',
        atob: 'readonly',
        btoa: 'readonly',
        process: 'readonly',
      },
    },
    rules: {
      // 未使用变量：下划线开头表示「有意不用」。
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      // 空对象类型 `{}` 在本项目里只出现在「泛型占位」的写法上，允许。
      '@typescript-eslint/no-empty-object-type': 'off',
      // 组件名用多词，避免和原生标签冲突（SingleFile 等内置组件除外）。
      'vue/multi-word-component-names': 'off',
      /*
       * 可选 prop 不必给默认值。
       *
       * 本项目的 prop 全部用 TS 类型声明，`note?: string` 已经把「可以不传」
       * 说清楚了；再补一个 `default: undefined` 只是重复，而且 `withDefaults`
       * 那套写法在泛型组件上会丢失类型。
       */
      'vue/require-default-prop': 'off',
      // 属性换行交给 prettier，lint 不管排版。
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/html-self-closing': 'off',
      'vue/html-indent': 'off',
      'vue/html-closing-bracket-newline': 'off',
      'vue/attributes-order': 'off',
      'vue/first-attribute-linebreak': 'off',
      'vue/multiline-html-element-content-newline': 'off',
    },
  },

  {
    // 用例里会用到全局测试 API。
    files: ['**/*.spec.ts'],
    languageOptions: {
      globals: { describe: 'readonly', it: 'readonly', expect: 'readonly', beforeEach: 'readonly' },
    },
    rules: {
      // 用例里用 defineComponent 造一次性组件是常事，不算「一个文件多个组件」。
      'vue/one-component-per-file': 'off',
    },
  },
)
