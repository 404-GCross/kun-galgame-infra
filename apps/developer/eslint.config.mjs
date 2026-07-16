import withNuxt from './.nuxt/eslint.config.mjs'
import a11y from 'eslint-plugin-vuejs-accessibility'

export default withNuxt(
  // Auto-generated tool output — never lint/format (regenerate, don't edit):
  // the bundled Tier-A public specs + the inline-SVG icon registry.
  { ignores: ['app/generated/**', 'app/assets/kun-icons.ts'] },
  // Apply the a11y plugin's recommended rules to all .vue files.
  // Documentation: https://vue-a11y.github.io/eslint-plugin-vuejs-accessibility/
  a11y.configs['flat/recommended'],
  {
    rules: {
      'no-console': 'off',
      camelcase: 'off',
      'comma-spacing': 'off',
      '@typescript-eslint/no-unused-vars': 'off',
      'vue/multi-word-component-names': 'off',
      'vue/singleline-html-element-content-newline': 'off',
      'vue/html-self-closing': 'off',
      'vue/attributes-order': 'off',
      'vue/no-multiple-template-root': 'off',
      'vue/no-v-html': 'off',
      'import/order': 'off',
      'import/no-named-as-default-member': 'off',
      'arrow-parens': ['error', 'always'],
      'space-before-function-paren': 'off',
      'func-call-spacing': 'off',
      quotes: [
        'error',
        'single',
        { avoidEscape: true, allowTemplateLiterals: true }
      ],
      'vuejs-accessibility/no-autofocus': 'off',
      'vuejs-accessibility/click-events-have-key-events': 'warn',
      'vuejs-accessibility/no-static-element-interactions': 'warn'
    }
  }
)
