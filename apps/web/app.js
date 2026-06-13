(function () {
  const AUTH_KIND = 22242;
  const DEFAULT_FILTER = {
    kinds: [1, 0, 10390],
    limit: 20,
  };

  const state = {
    relay: "",
    pubkey: "",
    socket: null,
    authed: false,
    authEventID: "",
    subID: "wrapster-web",
    pendingProfile: null,
  };

  const els = {
    connectionForm: document.getElementById("connection-form"),
    relayURL: document.getElementById("relay-url"),
    extensionStatus: document.getElementById("extension-status"),
    socketStatus: document.getElementById("socket-status"),
    pubkey: document.getElementById("pubkey"),
    keyButton: document.getElementById("key-button"),
    connectButton: document.getElementById("connect-button"),
    disconnectButton: document.getElementById("disconnect-button"),
    trustrootsUsername: document.getElementById("trustroots-username"),
    profileButton: document.getElementById("profile-button"),
    noteContent: document.getElementById("note-content"),
    publishButton: document.getElementById("publish-button"),
    filterJSON: document.getElementById("filter-json"),
    subscribeButton: document.getElementById("subscribe-button"),
    clearButton: document.getElementById("clear-button"),
    copyLogButton: document.getElementById("copy-log-button"),
    events: document.getElementById("events"),
    log: document.getElementById("log"),
  };

  init();

  function init() {
    els.relayURL.value = localStorage.getItem("wrapster.web.relay") || defaultRelayURL();
    els.filterJSON.value = JSON.stringify(DEFAULT_FILTER, null, 2);
    renderEmptyEvents();
    bindEvents();
    detectNIP07();
  }

  function bindEvents() {
    els.connectionForm.addEventListener("submit", (event) => {
      event.preventDefault();
      connect();
    });
    els.keyButton.addEventListener("click", loadPublicKey);
    els.disconnectButton.addEventListener("click", disconnect);
    els.profileButton.addEventListener("click", publishProfile);
    els.publishButton.addEventListener("click", publishNote);
    els.subscribeButton.addEventListener("click", sendSubscription);
    els.clearButton.addEventListener("click", renderEmptyEvents);
    els.copyLogButton.addEventListener("click", copyLog);
  }

  async function detectNIP07() {
    if (window.nostr || await waitForNostr()) {
      setPill(els.extensionStatus, "ok", "NIP-07 ready");
      await loadPublicKey();
      return;
    }
    setPill(els.extensionStatus, "bad", "NIP-07 unavailable");
    log("No NIP-07 signer found. Enable a browser extension that exposes window.nostr.");
  }

  function waitForNostr() {
    return new Promise((resolve) => {
      const started = Date.now();
      const timer = setInterval(() => {
        if (window.nostr) {
          clearInterval(timer);
          resolve(true);
          return;
        }
        if (Date.now() - started > 1500) {
          clearInterval(timer);
          resolve(false);
        }
      }, 100);
    });
  }

  async function loadPublicKey() {
    if (!window.nostr || typeof window.nostr.getPublicKey !== "function") {
      setPill(els.extensionStatus, "bad", "NIP-07 unavailable");
      return "";
    }
    try {
      const pubkey = String(await window.nostr.getPublicKey()).toLowerCase();
      if (!/^[0-9a-f]{64}$/.test(pubkey)) {
        throw new Error("Signer returned an invalid public key.");
      }
      state.pubkey = pubkey;
      els.pubkey.textContent = pubkey;
      els.pubkey.style.color = "var(--ink)";
      setPill(els.extensionStatus, "ok", "NIP-07 ready");
      log("NIP-07 key selected: " + shortKey(pubkey));
      return pubkey;
    } catch (error) {
      setPill(els.extensionStatus, "bad", "Key denied");
      log("Could not read NIP-07 key: " + errorMessage(error));
      return "";
    }
  }

  async function connect() {
    const relay = normalizeRelayURL(els.relayURL.value);
    if (!relay) {
      log("Enter a ws:// or wss:// relay URL.");
      return;
    }
    if (!hasSigner()) {
      log("NIP-07 signer is required for Wrapster relay authentication.");
      return;
    }
    if (!state.pubkey && !await loadPublicKey()) return;

    disconnect();
    state.relay = relay;
    state.authed = false;
    state.authEventID = "";
    localStorage.setItem("wrapster.web.relay", relay);
    els.relayURL.value = relay;
    setPill(els.socketStatus, "waiting", "Connecting");
    setControls();
    log("Connecting to " + relay);

    let socket;
    try {
      socket = new WebSocket(relay);
      state.socket = socket;
    } catch (error) {
      log("WebSocket failed: " + errorMessage(error));
      setPill(els.socketStatus, "bad", "Connection failed");
      setControls();
      return;
    }

    socket.addEventListener("open", () => {
      if (state.socket !== socket) return;
      setPill(els.socketStatus, "waiting", "Waiting for auth");
      log("WebSocket open");
      if (state.pendingProfile) {
        sendProfileEvent(state.pendingProfile);
      }
    });
    socket.addEventListener("message", (message) => {
      if (state.socket !== socket) return;
      handleRelayMessage(message);
    });
    socket.addEventListener("error", () => {
      if (state.socket !== socket) return;
      setPill(els.socketStatus, "bad", "Socket error");
      log("WebSocket error");
    });
    socket.addEventListener("close", () => {
      if (state.socket !== socket) return;
      state.socket = null;
      state.authed = false;
      state.authEventID = "";
      setPill(els.socketStatus, "neutral", "Disconnected");
      setControls();
      log("WebSocket closed");
    });
    setControls();
  }

  function disconnect() {
    const socket = state.socket;
    state.socket = null;
    state.authed = false;
    state.authEventID = "";
    if (socket && socket.readyState <= WebSocket.OPEN) {
      socket.close();
    }
    setPill(els.socketStatus, "neutral", "Disconnected");
    setControls();
  }

  async function handleRelayMessage(message) {
    let payload;
    try {
      payload = JSON.parse(message.data);
    } catch {
      log("Relay sent a non-JSON message.");
      return;
    }
    const type = payload[0];
    if (type === "AUTH") {
      await authenticate(String(payload[1] || ""));
      return;
    }
    if (type === "OK") {
      handleOK(payload);
      return;
    }
    if (type === "EVENT" && payload[1] === state.subID) {
      renderEvent(payload[2]);
      return;
    }
    if (type === "EOSE" && payload[1] === state.subID) {
      log("Subscription reached EOSE.");
      return;
    }
    if (type === "CLOSED") {
      log("Subscription closed: " + String(payload[2] || ""));
      return;
    }
    if (type === "NOTICE") {
      log("Relay notice: " + String(payload[1] || ""));
    }
  }

  async function authenticate(challenge) {
    if (!challenge) {
      log("Relay sent an empty auth challenge.");
      return;
    }
    try {
      const event = await window.nostr.signEvent({
        kind: AUTH_KIND,
        created_at: nowSeconds(),
        tags: [["relay", state.relay], ["challenge", challenge]],
        content: "",
      });
      state.authEventID = event.id || "";
      send(["AUTH", event]);
      setPill(els.socketStatus, "waiting", "Authenticating");
      log("Signed NIP-42 challenge as " + shortKey(event.pubkey));
    } catch (error) {
      setPill(els.socketStatus, "bad", "Auth signing denied");
      log("NIP-42 signing failed: " + errorMessage(error));
    }
  }

  function handleOK(payload) {
    const id = String(payload[1] || "");
    const ok = payload[2] === true;
    const reason = String(payload[3] || "");
    if (id && id === state.authEventID) {
      if (ok) {
        state.authed = true;
        setPill(els.socketStatus, "ok", "Authenticated");
        log("NIP-42 authentication accepted.");
        setControls();
        sendSubscription();
      } else {
        state.authed = false;
        setPill(els.socketStatus, "bad", "Auth rejected");
        log("NIP-42 authentication rejected: " + reason);
        setControls();
      }
      return;
    }
    log((ok ? "Event accepted: " : "Event rejected: ") + (reason || shortKey(id)));
  }

  async function publishProfile() {
    if (!hasSigner()) {
      log("NIP-07 signer is required to publish a profile.");
      return;
    }
    if (!state.pubkey && !await loadPublicKey()) return;
    const username = normalizeUsername(els.trustrootsUsername.value);
    if (!username) {
      log("Enter a Trustroots username with at least 3 characters.");
      return;
    }
    const content = JSON.stringify({
      name: username,
      display_name: username,
      trustrootsUsername: username,
      nip05: username + "@trustroots.org",
    });
    const unsigned = {
      kind: 0,
      created_at: nowSeconds(),
      tags: [],
      content,
    };

    try {
      const event = await window.nostr.signEvent(unsigned);
      if (isSocketOpen()) {
        send(["EVENT", event]);
      } else {
        state.pendingProfile = event;
        connect();
      }
      log("Profile event signed for " + username + "@trustroots.org");
    } catch (error) {
      log("Profile signing failed: " + errorMessage(error));
    }
  }

  function sendProfileEvent(event) {
    state.pendingProfile = null;
    send(["EVENT", event]);
    log("Profile event sent.");
  }

  async function publishNote() {
    if (!state.authed) {
      log("Connect and authenticate before publishing a note.");
      return;
    }
    const content = els.noteContent.value.trim();
    if (!content) {
      log("Write a note before sending.");
      return;
    }
    try {
      const event = await window.nostr.signEvent({
        kind: 1,
        created_at: nowSeconds(),
        tags: [],
        content,
      });
      send(["EVENT", event]);
      els.noteContent.value = "";
      log("Note signed and sent.");
    } catch (error) {
      log("Note signing failed: " + errorMessage(error));
    }
  }

  function sendSubscription() {
    if (!state.authed) {
      log("Connect and authenticate before subscribing.");
      return;
    }
    let filter;
    try {
      filter = JSON.parse(els.filterJSON.value);
    } catch (error) {
      log("Filter JSON is invalid: " + errorMessage(error));
      return;
    }
    if (!filter || typeof filter !== "object" || Array.isArray(filter)) {
      log("Filter must be a JSON object.");
      return;
    }
    send(["CLOSE", state.subID]);
    send(["REQ", state.subID, filter]);
    log("Subscription sent: " + JSON.stringify(filter));
  }

  function send(payload) {
    if (!isSocketOpen()) {
      log("WebSocket is not open.");
      return;
    }
    state.socket.send(JSON.stringify(payload));
  }

  function renderEvent(event) {
    if (!event || typeof event !== "object") return;
    const empty = els.events.querySelector(".empty");
    if (empty) empty.remove();

    const item = document.createElement("li");
    item.className = "event";

    const head = document.createElement("div");
    head.className = "event-head";
    const kind = document.createElement("span");
    kind.className = "event-kind";
    kind.textContent = "kind " + event.kind;
    const author = document.createElement("code");
    author.textContent = shortKey(event.pubkey || "");
    const date = document.createElement("span");
    date.textContent = formatTimestamp(event.created_at);
    head.append(kind, author, date);

    const content = document.createElement("p");
    content.className = "event-content";
    content.textContent = eventSummary(event);

    item.append(head, content);
    els.events.prepend(item);
  }

  function renderEmptyEvents() {
    els.events.replaceChildren();
    const empty = document.createElement("li");
    empty.className = "empty";
    empty.textContent = "No events yet";
    els.events.append(empty);
  }

  async function copyLog() {
    try {
      await navigator.clipboard.writeText(els.log.textContent);
      log("Log copied.");
    } catch (error) {
      log("Could not copy log: " + errorMessage(error));
    }
  }

  function setControls() {
    const open = isSocketOpen();
    els.connectButton.disabled = open && !state.authed;
    els.disconnectButton.disabled = !open;
    els.publishButton.disabled = !state.authed;
    els.subscribeButton.disabled = !state.authed;
  }

  function isSocketOpen() {
    return state.socket && state.socket.readyState === WebSocket.OPEN;
  }

  function hasSigner() {
    return window.nostr && typeof window.nostr.signEvent === "function";
  }

  function defaultRelayURL() {
    const host = window.location.hostname || "localhost";
    if (host === "localhost" || host === "127.0.0.1" || host === "::1") {
      return "ws://localhost:5542";
    }
    if (window.location.protocol === "https:") {
      return "wss://" + window.location.host;
    }
    return "ws://" + window.location.host;
  }

  function normalizeRelayURL(value) {
    const raw = String(value || "").trim();
    if (!raw) return "";
    try {
      const url = new URL(raw);
      if (url.protocol === "http:") url.protocol = "ws:";
      if (url.protocol === "https:") url.protocol = "wss:";
      if (url.protocol !== "ws:" && url.protocol !== "wss:") return "";
      url.hash = "";
      return url.toString().replace(/\/$/, "");
    } catch {
      return "";
    }
  }

  function normalizeUsername(value) {
    const username = String(value || "").trim().toLowerCase();
    return username.length >= 3 ? username : "";
  }

  function eventSummary(event) {
    if (event.kind === 0) {
      try {
        const profile = JSON.parse(event.content || "{}");
        return profile.nip05 || profile.trustrootsUsername || profile.name || event.content || "Profile";
      } catch {
        return event.content || "Profile";
      }
    }
    return event.content || JSON.stringify(event.tags || []);
  }

  function setPill(el, kind, text) {
    el.className = "pill " + kind;
    el.textContent = text;
  }

  function log(message) {
    const stamp = new Date().toISOString().slice(11, 19);
    els.log.textContent += "[" + stamp + "] " + message + "\n";
    els.log.scrollTop = els.log.scrollHeight;
  }

  function nowSeconds() {
    return Math.floor(Date.now() / 1000);
  }

  function shortKey(value) {
    const key = String(value || "");
    if (key.length <= 16) return key || "unknown";
    return key.slice(0, 8) + "..." + key.slice(-8);
  }

  function formatTimestamp(value) {
    const seconds = Number(value);
    if (!Number.isFinite(seconds) || seconds <= 0) return "";
    return new Date(seconds * 1000).toLocaleString();
  }

  function errorMessage(error) {
    return error && error.message ? error.message : String(error);
  }
})();
