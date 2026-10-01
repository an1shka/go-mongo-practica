const authView = document.querySelector("#auth-view");
const chatView = document.querySelector("#chat-view");
const authForm = document.querySelector("#auth-form");
const authFeedback = document.querySelector("#auth-feedback");
const chatFeedback = document.querySelector("#chat-feedback");
const authSubmit = document.querySelector("#auth-submit");
const formTitle = document.querySelector("#form-title");
const formDescription = document.querySelector("#form-description");
const passwordInput = document.querySelector("#password");
const usernameInput = document.querySelector("#username");
const tabs = [...document.querySelectorAll(".auth-tab")];
const messageList = document.querySelector("#message-list");
const emptyState = document.querySelector("#empty-state");
const messageForm = document.querySelector("#message-form");
const messageInput = document.querySelector("#message-input");
const sendButton = document.querySelector("#send-button");
const connectionStatus = document.querySelector("#connection-status");
const connectionLabel = document.querySelector("#connection-label");
const currentUsername = document.querySelector("#current-username");
const userAvatar = document.querySelector("#user-avatar");

const TOKEN_KEY = "mini-chat-token";
const USERNAME_KEY = "mini-chat-username";
const messagesById = new Map();
let mode = "login";
let socket = null;
let reconnectTimer = null;
let reconnectDelay = 1000;
let isSubmitting = false;

function showFeedback(element, message, success = false) {
  element.textContent = message;
  element.classList.toggle("is-success", success);
}

function setMode(nextMode) {
  mode = nextMode;
  const registering = mode === "register";
  tabs.forEach((tab) => {
    const active = tab.dataset.mode === mode;
    tab.classList.toggle("is-active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  formTitle.textContent = registering ? "Создайте аккаунт" : "С возвращением";
  formDescription.textContent = registering
    ? "Имя и пароль понадобятся для входа в чат."
    : "Введите данные, чтобы открыть чат.";
  authSubmit.textContent = registering ? "Зарегистрироваться" : "Войти в чат";
  passwordInput.autocomplete = registering ? "new-password" : "current-password";
  passwordInput.minLength = registering ? 8 : 1;
  showFeedback(authFeedback, "");
}

function showAuth() {
  authView.hidden = false;
  chatView.hidden = true;
  document.title = "Mini Chat — вход";
  setConnection("disconnected", "Не подключён");
}

function showChat(username) {
  authView.hidden = true;
  chatView.hidden = false;
  currentUsername.textContent = username;
  userAvatar.textContent = (Array.from(username)[0] || "M").toLocaleUpperCase("ru");
  document.title = "Mini Chat — общая комната";
  loadHistory();
  connectSocket();
  messageInput.focus();
}

function setConnection(state, label) {
  connectionStatus.classList.remove(
    "is-connected",
    "is-disconnected",
    "is-connecting",
  );
  connectionStatus.classList.add(`is-${state}`);
  connectionLabel.textContent = label;
}

async function requestJSON(path, options = {}) {
  const token = sessionStorage.getItem(TOKEN_KEY);
  const headers = new Headers(options.headers || {});
  if (options.body) headers.set("Content-Type", "application/json");
  if (token && options.auth !== false) headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(path, { ...options, headers });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    if (response.status === 401 && token) logout(false);
    throw new Error(data.error || "Не удалось выполнить запрос");
  }
  return data;
}

async function loadHistory() {
  try {
    const messages = await requestJSON("/messages");
    messages.forEach(addMessage);
    scrollToLatest();
  } catch (error) {
    showFeedback(chatFeedback, error.message);
  }
}

function addMessage(message) {
  if (!message || !message.id || messagesById.has(message.id)) return;
  messagesById.set(message.id, message);
  emptyState.hidden = true;

  const ownMessage = message.username === sessionStorage.getItem(USERNAME_KEY);
  const row = document.createElement("article");
  row.className = `message-row${ownMessage ? " is-own" : ""}`;
  row.dataset.messageId = message.id;

  const avatar = document.createElement("span");
  avatar.className = "message-avatar";
  avatar.setAttribute("aria-hidden", "true");
  avatar.textContent = (Array.from(message.username || "?")[0] || "?").toLocaleUpperCase("ru");

  const content = document.createElement("div");
  content.className = "message-content";
  const meta = document.createElement("div");
  meta.className = "message-meta";
  const name = document.createElement("strong");
  name.textContent = ownMessage ? "Вы" : message.username;
  const time = document.createElement("time");
  const date = new Date(message.created_at);
  time.dateTime = Number.isNaN(date.getTime()) ? "" : date.toISOString();
  time.textContent = Number.isNaN(date.getTime())
    ? ""
    : new Intl.DateTimeFormat("ru", { hour: "2-digit", minute: "2-digit" }).format(date);
  meta.append(name, time);

  const bubble = document.createElement("div");
  bubble.className = "message-bubble";
  bubble.textContent = message.text;
  content.append(meta, bubble);
  row.append(avatar, content);
  messageList.append(row);
  scrollToLatest();
}

function scrollToLatest() {
  messageList.scrollTop = messageList.scrollHeight;
}

function connectSocket() {
  clearTimeout(reconnectTimer);
  const token = sessionStorage.getItem(TOKEN_KEY);
  if (!token) return;
  setConnection("connecting", "Подключаемся");

  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url = `${protocol}//${window.location.host}/ws?token=${encodeURIComponent(token)}`;
  socket = new WebSocket(url);

  socket.addEventListener("open", () => {
    reconnectDelay = 1000;
    setConnection("connected", "В сети");
    showFeedback(chatFeedback, "");
    // Подтягиваем пропущенные сообщения после каждого переподключения.
    loadHistory();
  });
  socket.addEventListener("message", (event) => {
    try {
      const payload = JSON.parse(event.data);
      if (payload.error) {
        showFeedback(chatFeedback, payload.error);
        return;
      }
      addMessage(payload);
    } catch {
      showFeedback(chatFeedback, "Получено сообщение в неизвестном формате");
    }
  });
  socket.addEventListener("error", () => {
    setConnection("disconnected", "Нет соединения");
  });
  socket.addEventListener("close", () => {
    if (!sessionStorage.getItem(TOKEN_KEY)) return;
    setConnection("disconnected", "Переподключаемся");
    reconnectTimer = window.setTimeout(() => {
      reconnectDelay = Math.min(reconnectDelay * 2, 10000);
      connectSocket();
    }, reconnectDelay);
  });
}

async function logout(showMessage = true) {
  sessionStorage.removeItem(TOKEN_KEY);
  sessionStorage.removeItem(USERNAME_KEY);
  clearTimeout(reconnectTimer);
  reconnectTimer = null;
  if (socket) {
    socket.close(1000, "logout");
    socket = null;
  }
  messagesById.clear();
  messageList.querySelectorAll(".message-row").forEach((message) => message.remove());
  emptyState.hidden = false;
  showAuth();
  if (showMessage) showFeedback(authFeedback, "Вы вышли из чата.", true);
}

tabs.forEach((tab) => tab.addEventListener("click", () => setMode(tab.dataset.mode)));

authForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (isSubmitting) return;
  showFeedback(authFeedback, "");
  const username = usernameInput.value.trim();
  const password = passwordInput.value;
  const endpoint = mode === "register" ? "/register" : "/login";
  isSubmitting = true;
  authSubmit.disabled = true;
  authSubmit.textContent = mode === "register" ? "Создаём аккаунт..." : "Входим...";

  try {
    const result = await requestJSON(endpoint, {
      method: "POST",
      auth: false,
      body: JSON.stringify({ username, password }),
    });
    if (mode === "register") {
      setMode("login");
      usernameInput.value = username;
      passwordInput.value = "";
      showFeedback(authFeedback, result.message || "Аккаунт создан. Теперь войдите.", true);
    } else {
      sessionStorage.setItem(TOKEN_KEY, result.token);
      sessionStorage.setItem(USERNAME_KEY, result.username);
      showChat(result.username);
    }
  } catch (error) {
    showFeedback(authFeedback, error.message);
  } finally {
    isSubmitting = false;
    authSubmit.disabled = false;
    authSubmit.textContent = mode === "register" ? "Зарегистрироваться" : "Войти в чат";
  }
});

document.querySelector("#logout-button").addEventListener("click", () => logout());

messageForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const text = messageInput.value.trim();
  if (!text || text.length > 2000) return;
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    showFeedback(chatFeedback, "Соединение прервано. Дождитесь подключения и попробуйте снова.");
    return;
  }
  socket.send(JSON.stringify({ text }));
  messageInput.value = "";
  messageInput.style.height = "auto";
  showFeedback(chatFeedback, "");
});

messageInput.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    messageForm.requestSubmit();
  }
});

messageInput.addEventListener("input", () => {
  messageInput.style.height = "auto";
  messageInput.style.height = `${Math.min(messageInput.scrollHeight, 130)}px`;
});

const savedToken = sessionStorage.getItem(TOKEN_KEY);
const savedUsername = sessionStorage.getItem(USERNAME_KEY);
if (savedToken && savedUsername) {
  showChat(savedUsername);
} else {
  showAuth();
}