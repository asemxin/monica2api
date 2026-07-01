package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func serveWebGUI(c echo.Context) error {
	return c.HTML(http.StatusOK, webGUIHTML)
}

const webGUIHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Monica2API</title>
  <style>
    :root {
      --bg: #f6f7f9;
      --panel: #fff;
      --line: #d9dee7;
      --text: #172033;
      --muted: #657086;
      --accent: #1463ff;
      --accent-dark: #0f48bf;
      --danger: #b42318;
      --shadow: 0 18px 45px rgba(23, 32, 51, 0.10);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: var(--bg);
      color: var(--text);
      min-height: 100vh;
    }
    .app {
      min-height: 100vh;
      display: grid;
      grid-template-rows: auto 1fr auto;
    }
    header {
      min-height: 64px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      padding: 0 24px;
      border-bottom: 1px solid var(--line);
      background: rgba(255, 255, 255, 0.94);
      backdrop-filter: blur(14px);
      position: sticky;
      top: 0;
      z-index: 2;
    }
    h1 {
      margin: 0;
      font-size: 18px;
      font-weight: 720;
      letter-spacing: 0;
    }
    .status {
      color: var(--muted);
      font-size: 13px;
      min-width: 0;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    main {
      width: min(1120px, calc(100% - 32px));
      margin: 24px auto;
      display: grid;
      grid-template-columns: 320px minmax(0, 1fr);
      gap: 16px;
      min-height: calc(100vh - 160px);
    }
    aside, .chat {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      box-shadow: var(--shadow);
    }
    aside {
      padding: 16px;
      display: flex;
      flex-direction: column;
      gap: 14px;
      height: fit-content;
    }
    label {
      display: block;
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
      margin: 0 0 7px;
    }
    input, select, textarea {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 6px;
      padding: 11px 12px;
      font: inherit;
      color: var(--text);
      background: #fff;
      outline: none;
    }
    textarea {
      min-height: 52px;
      resize: vertical;
      line-height: 1.5;
    }
    input:focus, select:focus, textarea:focus {
      border-color: var(--accent);
      box-shadow: 0 0 0 3px rgba(20, 99, 255, 0.12);
    }
    .row {
      display: grid;
      grid-template-columns: 1fr 96px;
      gap: 8px;
      align-items: end;
    }
    button {
      border: 0;
      border-radius: 6px;
      padding: 11px 14px;
      font: inherit;
      font-weight: 720;
      color: #fff;
      background: var(--accent);
      cursor: pointer;
      min-height: 44px;
    }
    button:hover { background: var(--accent-dark); }
    button:disabled {
      opacity: 0.58;
      cursor: not-allowed;
    }
    .ghost {
      background: #edf2ff;
      color: var(--accent-dark);
    }
    .ghost:hover { background: #dfe8ff; }
    .hint {
      color: var(--muted);
      font-size: 12px;
      line-height: 1.45;
      margin: 0;
    }
    .chat {
      min-height: 560px;
      display: grid;
      grid-template-rows: 1fr auto;
      overflow: hidden;
    }
    .messages {
      padding: 20px;
      overflow: auto;
      display: flex;
      flex-direction: column;
      gap: 14px;
    }
    .empty {
      height: 100%;
      min-height: 360px;
      display: grid;
      place-items: center;
      color: var(--muted);
      text-align: center;
      line-height: 1.5;
    }
    .message {
      max-width: 82%;
      padding: 13px 14px;
      border-radius: 8px;
      line-height: 1.55;
      white-space: pre-wrap;
      word-break: break-word;
      border: 1px solid var(--line);
      background: #fff;
    }
    .user {
      align-self: flex-end;
      color: #fff;
      background: var(--accent);
      border-color: var(--accent);
    }
    .assistant {
      align-self: flex-start;
      background: #fbfcff;
    }
    .error {
      align-self: stretch;
      max-width: 100%;
      color: var(--danger);
      background: #fff5f5;
      border-color: #ffd6d1;
    }
    form {
      border-top: 1px solid var(--line);
      padding: 14px;
      display: grid;
      grid-template-columns: 1fr 112px;
      gap: 10px;
      background: #fff;
    }
    footer {
      color: var(--muted);
      font-size: 12px;
      padding: 0 24px 20px;
      text-align: center;
    }
    @media (max-width: 820px) {
      header {
        align-items: flex-start;
        flex-direction: column;
        padding: 14px 16px;
      }
      main {
        grid-template-columns: 1fr;
        width: min(100% - 24px, 720px);
        margin: 14px auto;
      }
      .chat { min-height: 520px; }
      .message { max-width: 94%; }
      form, .row { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <div class="app">
    <header>
      <h1>Monica2API</h1>
      <div class="status" id="status">Ready</div>
    </header>
    <main>
      <aside>
        <div>
          <label for="apiKey">API Key</label>
          <input id="apiKey" type="password" autocomplete="off" placeholder="Bearer token">
        </div>
        <div class="row">
          <div>
            <label for="model">Model</label>
            <select id="model"></select>
          </div>
          <button class="ghost" id="loadModels" type="button">Load</button>
        </div>
        <div>
          <label for="systemPrompt">System Prompt</label>
          <textarea id="systemPrompt" placeholder="Optional"></textarea>
        </div>
        <button class="ghost" id="clearChat" type="button">Clear Chat</button>
        <p class="hint">The API key is stored only in this browser. API calls still use the protected /v1 endpoints.</p>
      </aside>
      <section class="chat">
        <div class="messages" id="messages">
          <div class="empty" id="empty">Enter your API key, load models, and send a message.</div>
        </div>
        <form id="chatForm">
          <textarea id="prompt" placeholder="Type a message..." required></textarea>
          <button id="sendButton" type="submit">Send</button>
        </form>
      </section>
    </main>
    <footer>Base URL: <span id="baseUrl"></span>/v1</footer>
  </div>
  <script>
    const apiKeyEl = document.getElementById('apiKey');
    const modelEl = document.getElementById('model');
    const loadModelsEl = document.getElementById('loadModels');
    const systemPromptEl = document.getElementById('systemPrompt');
    const clearChatEl = document.getElementById('clearChat');
    const chatForm = document.getElementById('chatForm');
    const promptEl = document.getElementById('prompt');
    const sendButton = document.getElementById('sendButton');
    const messagesEl = document.getElementById('messages');
    const emptyEl = document.getElementById('empty');
    const statusEl = document.getElementById('status');
    const baseUrlEl = document.getElementById('baseUrl');
    let messages = [];

    baseUrlEl.textContent = window.location.origin;
    apiKeyEl.value = localStorage.getItem('monica2api_key') || '';
    systemPromptEl.value = localStorage.getItem('monica2api_system') || '';

    function setStatus(text) {
      statusEl.textContent = text;
    }
    function authHeaders() {
      const token = apiKeyEl.value.trim();
      if (!token) throw new Error('Missing API key');
      localStorage.setItem('monica2api_key', token);
      localStorage.setItem('monica2api_system', systemPromptEl.value);
      return {
        'Authorization': 'Bearer ' + token,
        'Content-Type': 'application/json'
      };
    }
    function addMessage(role, content) {
      emptyEl?.remove();
      const item = document.createElement('div');
      item.className = 'message ' + role;
      item.textContent = content;
      messagesEl.appendChild(item);
      messagesEl.scrollTop = messagesEl.scrollHeight;
    }
    function addError(error) {
      addMessage('error', error.message || String(error));
    }
    async function loadModels() {
      try {
        setStatus('Loading models...');
        loadModelsEl.disabled = true;
        const res = await fetch('/v1/models', { headers: authHeaders() });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        modelEl.innerHTML = '';
        for (const item of data.data || []) {
          const option = document.createElement('option');
          option.value = item.id;
          option.textContent = item.id;
          modelEl.appendChild(option);
        }
        modelEl.value = localStorage.getItem('monica2api_model') || modelEl.value;
        setStatus('Models loaded');
      } catch (error) {
        setStatus('Model load failed');
        addError(error);
      } finally {
        loadModelsEl.disabled = false;
      }
    }
    async function sendMessage(event) {
      event.preventDefault();
      const text = promptEl.value.trim();
      if (!text) return;
      const model = modelEl.value || 'gpt-4o';
      localStorage.setItem('monica2api_model', model);
      messages.push({ role: 'user', content: text });
      addMessage('user', text);
      promptEl.value = '';
      sendButton.disabled = true;
      setStatus('Thinking...');
      const requestMessages = [];
      const system = systemPromptEl.value.trim();
      if (system) requestMessages.push({ role: 'system', content: system });
      requestMessages.push(...messages);
      try {
        const res = await fetch('/v1/chat/completions', {
          method: 'POST',
          headers: authHeaders(),
          body: JSON.stringify({ model, messages: requestMessages, stream: false })
        });
        if (!res.ok) throw new Error(await res.text());
        const data = await res.json();
        const reply = data.choices?.[0]?.message?.content || JSON.stringify(data, null, 2);
        messages.push({ role: 'assistant', content: reply });
        addMessage('assistant', reply);
        setStatus('Ready');
      } catch (error) {
        setStatus('Request failed');
        addError(error);
      } finally {
        sendButton.disabled = false;
        promptEl.focus();
      }
    }
    loadModelsEl.addEventListener('click', loadModels);
    chatForm.addEventListener('submit', sendMessage);
    clearChatEl.addEventListener('click', () => {
      messages = [];
      messagesEl.innerHTML = '<div class="empty" id="empty">Enter your API key, load models, and send a message.</div>';
      setStatus('Ready');
    });
    apiKeyEl.addEventListener('change', () => localStorage.setItem('monica2api_key', apiKeyEl.value.trim()));
    systemPromptEl.addEventListener('change', () => localStorage.setItem('monica2api_system', systemPromptEl.value));
    promptEl.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) chatForm.requestSubmit();
    });
    if (apiKeyEl.value) loadModels();
  </script>
</body>
</html>`
