<template>
  <div class="rich-text-editor">
    <Toolbar class="rich-text-editor__toolbar" :editor="editorRef" :default-config="toolbarConfig" mode="default" />
    <Editor
      class="rich-text-editor__content"
      :model-value="modelValue"
      :default-config="editorConfig"
      mode="default"
      @on-created="handleCreated"
      @on-change="handleChange"
    />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, shallowRef } from 'vue';
import '@wangeditor/editor/dist/css/style.css';
import { Editor, Toolbar } from '@wangeditor/editor-for-vue';
import type { IDomEditor, IEditorConfig, IToolbarConfig } from '@wangeditor/editor';

const props = withDefaults(
  defineProps<{
    modelValue: string;
    placeholder?: string;
    height?: number;
  }>(),
  {
    placeholder: '',
    height: 420,
  },
);

const emit = defineEmits<{
  'update:modelValue': [value: string];
}>();

const editorRef = shallowRef<IDomEditor>();

const toolbarConfig: Partial<IToolbarConfig> = {
  toolbarKeys: [
    'headerSelect',
    '|',
    'bold',
    'italic',
    'underline',
    'through',
    'clearStyle',
    '|',
    'bulletedList',
    'numberedList',
    'blockquote',
    'divider',
    '|',
    'insertTable',
    '|',
    'undo',
    'redo',
  ],
};

const editorConfig: Partial<IEditorConfig> = {
  placeholder: props.placeholder,
};

const handleCreated = (editor: IDomEditor) => {
  editorRef.value = editor;
};

const handleChange = (editor: IDomEditor) => {
  emit('update:modelValue', editor.isEmpty() ? '' : editor.getHtml());
};

onBeforeUnmount(() => {
  const editor = editorRef.value;
  if (editor) {
    editor.destroy();
  }
});
</script>

<style scoped>
.rich-text-editor {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-border);
  border-radius: var(--td-radius-default);
  overflow: hidden;
}

.rich-text-editor__toolbar {
  border-bottom: 1px solid var(--td-component-border);
}

.rich-text-editor__content {
  height: v-bind('`${height}px`') !important;
  overflow-y: hidden;
}
</style>
