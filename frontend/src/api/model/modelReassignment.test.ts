import assert from 'node:assert/strict'
import test from 'node:test'

import {
  ModelReassignmentBlockedError,
  modelReassignmentPlanFromRequest,
  parseModelReassignmentPlan,
} from './modelUsage'

const planPayload = {
  model_id: 'model-old',
  model_name: 'Old Model',
  can_delete: true,
  items: [
    {
      binding: 'summary_model',
      model_type: 'KnowledgeQA',
      target_model_id: 'model-chat',
      target_model_name: 'Default Chat',
      target_source: 'workspace_default',
      knowledge_base_count: 3,
      agent_count: 0,
      long_term_memory: false,
    },
  ],
  blockers: [],
  knowledge_base_total: 3,
  agent_total: 0,
  long_term_memory: { bindings: [] },
  session_model_count: 2,
  session_target_model_id: 'model-chat',
  session_target_model_name: 'Default Chat',
}

test('parses a reassignment plan with replacement items', () => {
  const plan = parseModelReassignmentPlan(planPayload)
  assert.ok(plan)
  assert.equal(plan.can_delete, true)
  assert.equal(plan.model_name, 'Old Model')
  assert.equal(plan.items.length, 1)
  assert.equal(plan.items[0].target_model_id, 'model-chat')
  assert.equal(plan.items[0].knowledge_base_count, 3)
  assert.deepEqual(plan.blockers, [])
  assert.equal(plan.session_model_count, 2)
  assert.equal(plan.session_target_model_id, 'model-chat')
})

test('parses embedding reference blockers', () => {
  const plan = parseModelReassignmentPlan({
    ...planPayload,
    can_delete: false,
    items: [],
    blockers: [
      { code: 'embedding_model_in_documents', binding: 'embedding_model', document_count: 4 },
      { code: 'embedding_model_in_chat_history', binding: 'embedding_model' },
    ],
  })
  assert.ok(plan)
  assert.equal(plan.blockers.length, 2)
  assert.equal(plan.blockers[0].document_count, 4)
  assert.equal(plan.blockers[1].code, 'embedding_model_in_chat_history')
})

test('parses blockers and keeps the plan non-deletable', () => {
  const plan = parseModelReassignmentPlan({
    ...planPayload,
    can_delete: false,
    items: [],
    blockers: [
      { code: 'embedding_model_has_documents', binding: 'embedding_model', knowledge_base_count: 2 },
      { code: 'no_default_model', binding: 'vlm_model', model_type: 'VLLM' },
    ],
  })
  assert.ok(plan)
  assert.equal(plan.can_delete, false)
  assert.equal(plan.blockers.length, 2)
  assert.equal(plan.blockers[0].code, 'embedding_model_has_documents')
  assert.equal(plan.blockers[0].knowledge_base_count, 2)
})

test('rejects malformed plans instead of trusting partial payloads', () => {
  assert.equal(parseModelReassignmentPlan(null), null)
  assert.equal(parseModelReassignmentPlan({}), null)
  assert.equal(parseModelReassignmentPlan({ ...planPayload, can_delete: 'yes' }), null)
  assert.equal(parseModelReassignmentPlan({ ...planPayload, items: [{ binding: 'chat_model' }] }), null)
})

test('extracts a plan from a blocked deletion response', () => {
  const plan = modelReassignmentPlanFromRequest({
    error: { code: 400, message: 'blocked', details: planPayload },
  })
  assert.ok(plan)
  assert.equal(plan.model_id, 'model-old')

  const error = new ModelReassignmentBlockedError(plan)
  assert.equal(error.name, 'ModelReassignmentBlockedError')
  assert.equal(error.plan.can_delete, true)
})
