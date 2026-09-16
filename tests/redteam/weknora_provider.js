const DEFAULT_BASE_URL = 'http://localhost:8082';
const REQUEST_TIMEOUT_MS = 300_000;
const LOGIN_TIMEOUT_MS = 30_000;

function parseSse(raw) {
  let answer = '';
  let streamError = null;
  for (const line of raw.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed.startsWith('data:')) continue;
    const payload = trimmed.slice(5).trim();
    if (!payload || payload === '[DONE]') continue;
    let evt;
    try {
      evt = JSON.parse(payload);
    } catch {
      continue;
    }
    if (evt.response_type === 'answer') {
      answer += evt.content || '';
    } else if (evt.response_type === 'error') {
      streamError = evt.content || 'unknown stream error';
    }
  }
  return { answer, streamError };
}

function extractUserTexts(prompt) {
  const trimmed = String(prompt).trim();
  if (trimmed.startsWith('[')) {
    try {
      const messages = JSON.parse(trimmed);
      if (Array.isArray(messages)) {
        const texts = messages
          .filter((m) => m && m.role === 'user')
          .map((m) => (typeof m.content === 'string' ? m.content : JSON.stringify(m.content)));
        if (texts.length > 0) return texts;
      }
    } catch {
      // fall through: treat as plain text
    }
  }
  return [String(prompt)];
}

class WeknoraProvider {
  constructor(options) {
    this.providerConfig = (options && options.config) || {};
    this.baseUrl = String(
      this.providerConfig.baseUrl || process.env.WEKNORA_BASE_URL || DEFAULT_BASE_URL,
    ).replace(/\/+$/, '');
    this.token = null;
  }

  id() {
    return 'weknora-knowledge-chat';
  }

  async login() {
    const email = process.env.WEKNORA_EMAIL;
    const password = process.env.WEKNORA_PASSWORD;
    if (!email || !password) {
      throw new Error('missing env WEKNORA_EMAIL / WEKNORA_PASSWORD');
    }
    const resp = await fetch(`${this.baseUrl}/api/v1/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
      signal: AbortSignal.timeout(LOGIN_TIMEOUT_MS),
    });
    if (!resp.ok) {
      throw new Error(`login failed: HTTP ${resp.status} ${await resp.text()}`);
    }
    const data = await resp.json();
    if (!data.success || !data.token) {
      throw new Error(`login failed: ${JSON.stringify(data)}`);
    }
    this.token = data.token;
  }

  async apiFetch(path, init, allowRetry = true) {
    if (!this.token) await this.login();
    const resp = await fetch(`${this.baseUrl}${path}`, {
      ...init,
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${this.token}`,
        ...((init && init.headers) || {}),
      },
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
    if (resp.status === 401 && allowRetry) {
      this.token = null;
      return this.apiFetch(path, init, false);
    }
    return resp;
  }

  async createSession() {
    const resp = await this.apiFetch('/api/v1/sessions', {
      method: 'POST',
      body: JSON.stringify({ title: `redteam-${Date.now()}` }),
    });
    if (!resp.ok) {
      throw new Error(`create session failed: HTTP ${resp.status} ${await resp.text()}`);
    }
    const data = await resp.json();
    const sessionId = data && data.data && data.data.id;
    if (!sessionId) {
      throw new Error(`create session response missing id: ${JSON.stringify(data)}`);
    }
    return sessionId;
  }

  async ask(sessionId, query) {
    const body = { query, disable_title: true };
    const kbIds = (process.env.WEKNORA_KB_IDS || '')
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    if (kbIds.length > 0) body.knowledge_base_ids = kbIds;
    if (process.env.WEKNORA_AGENT_ID) {
      body.agent_id = process.env.WEKNORA_AGENT_ID;
      body.agent_enabled = true;
    }
    const resp = await this.apiFetch(`/api/v1/knowledge-chat/${sessionId}`, {
      method: 'POST',
      body: JSON.stringify(body),
    });
    if (!resp.ok) {
      throw new Error(`knowledge-chat failed: HTTP ${resp.status} ${await resp.text()}`);
    }
    const raw = await resp.text();
    const { answer, streamError } = parseSse(raw);
    if (streamError) {
      throw new Error(`stream error event: ${streamError}`);
    }
    if (!answer) {
      throw new Error('no answer content in SSE stream');
    }
    return answer;
  }

  async callApi(prompt) {
    try {
      const sessionId = await this.createSession();
      const userTexts = extractUserTexts(prompt);
      let output = '';
      for (const text of userTexts) {
        output = await this.ask(sessionId, text);
      }
      return { output, metadata: { sessionId } };
    } catch (err) {
      return { error: String((err && err.message) || err) };
    }
  }
}

module.exports = WeknoraProvider;
