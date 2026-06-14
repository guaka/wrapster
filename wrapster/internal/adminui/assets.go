package adminui

import "strings"

const commonCSS = `
:root {
  color-scheme: dark;
  --bg: #080a0f;
  --fg: #edf7ff;
  --muted: #93a4b8;
  --muted-2: #607186;
  --line: #203041;
  --line-hot: #19f3ff;
  --panel: #0d121b;
  --panel-soft: #111927;
  --panel-deep: #090d14;
  --accent: #19f3ff;
  --accent-2: #ff3df2;
  --accent-soft: rgba(25, 243, 255, .12);
  --danger: #ff496d;
  --danger-soft: rgba(255, 73, 109, .13);
  --warn: #ffd166;
  --warn-soft: rgba(255, 209, 102, .13);
  --shadow: 0 18px 48px rgb(0 0 0 / .42);
  --mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  min-height: 100vh;
  background:
    linear-gradient(rgba(25, 243, 255, .035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 61, 242, .03) 1px, transparent 1px),
    linear-gradient(180deg, rgba(25, 243, 255, .08), rgba(8, 10, 15, 0) 260px),
    var(--bg);
  background-size: 28px 28px, 28px 28px, auto, auto;
  color: var(--fg);
  font: 14px/1.45 Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
body::before {
  content: "";
  position: fixed;
  inset: 0;
  pointer-events: none;
  background: repeating-linear-gradient(180deg, rgba(255,255,255,.025), rgba(255,255,255,.025) 1px, transparent 1px, transparent 4px);
  mix-blend-mode: screen;
}
button,
input,
select,
textarea {
  font: inherit;
}
header,
main,
.site-footer {
  width: min(100% - 32px, 1560px);
  margin: 0 auto;
}
header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 18px;
  padding: 22px 0 18px;
  border-bottom: 1px solid var(--line);
}
main {
  padding: 16px 0 34px;
}
.brand-block {
  display: grid;
  gap: 6px;
  min-width: 0;
}
h1,
h2,
h3 {
  margin: 0;
  letter-spacing: 0;
}
h1 {
  color: #ffffff;
  font-size: clamp(28px, 3.4vw, 48px);
  line-height: 1;
  font-weight: 850;
  text-shadow: 0 0 24px rgba(25, 243, 255, .28);
}
h2 {
  margin-bottom: 14px;
  color: #ffffff;
  font-size: 18px;
  line-height: 1.2;
  font-weight: 800;
}
h3 {
  color: #ffffff;
  font-size: 14px;
  line-height: 1.2;
  font-weight: 780;
}
.status,
.muted-line {
  color: var(--muted);
  overflow-wrap: anywhere;
}
.policy-note,
.identity-line {
  display: none;
}
.hidden {
  display: none !important;
}
.toolbar,
.actions,
.form-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 10px;
}
.header-status {
  display: flex;
  align-items: flex-end;
  flex-direction: column;
  gap: 12px;
  min-width: min(520px, 45vw);
}
button {
  min-height: 38px;
  border: 1px solid var(--accent);
  border-radius: 8px;
  background: var(--accent);
  color: #041016;
  cursor: pointer;
  font-weight: 800;
  padding: 0 14px;
  box-shadow: 0 0 18px rgba(25, 243, 255, .16);
}
button:hover:not(:disabled) {
  border-color: #ffffff;
  box-shadow: 0 0 24px rgba(25, 243, 255, .28), inset 0 0 0 1px rgba(255,255,255,.18);
}
button.secondary {
  background: rgba(25, 243, 255, .04);
  color: var(--accent);
}
button:disabled {
  cursor: not-allowed;
  opacity: .5;
}
.connect-button,
.connect-button.connected {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  max-width: min(560px, 100%);
  text-align: left;
}
.connect-button.connected {
  cursor: default;
}
.connect-button.connected:disabled {
  opacity: 1;
}
.connect-dot,
.status-dot {
  flex: 0 0 auto;
  width: 11px;
  height: 11px;
  border-radius: 999px;
  background: var(--muted-2);
  box-shadow: 0 0 0 4px rgba(96, 113, 134, .18);
}
.connect-dot.ok,
.status-dot.ok {
  background: var(--accent);
  box-shadow: 0 0 0 4px var(--accent-soft), 0 0 16px rgba(25, 243, 255, .55);
}
.connect-dot.bad,
.status-dot.bad {
  background: var(--danger);
  box-shadow: 0 0 0 4px var(--danger-soft), 0 0 16px rgba(255, 73, 109, .45);
}
.connect-label {
  min-width: 0;
  overflow-wrap: anywhere;
}
.connect-status {
  color: var(--muted);
  font-size: 12px;
  text-align: right;
}
.connect-status.ok { color: var(--accent); }
.connect-status.bad { color: var(--danger); }
.connect-status.neutral { color: var(--muted); }
.fips-header-status {
  width: min(480px, 100%);
  border: 1px solid var(--line);
  border-radius: 999px;
  background: rgba(13, 18, 27, .84);
  color: var(--muted);
  font-size: 12px;
  overflow: hidden;
  padding: 7px 12px;
  text-align: right;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fips-header-status.ok {
  border-color: rgba(25, 243, 255, .58);
  background: var(--accent-soft);
  color: var(--accent);
}
.fips-header-status.bad {
  border-color: rgba(255, 73, 109, .58);
  background: var(--danger-soft);
  color: var(--danger);
}
.fips-header-status.neutral {
  border-color: var(--line);
  color: var(--muted);
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 14px;
}
section,
.dashboard-card,
.advert-card,
.query-entry,
.access-rule-card {
  border: 1px solid var(--line);
  border-radius: 8px;
  background: linear-gradient(180deg, rgba(17, 25, 39, .96), rgba(9, 13, 20, .96));
  box-shadow: var(--shadow);
  min-width: 0;
}
section {
  padding: 18px;
}
.wide {
  grid-column: 1 / -1;
}
label {
  display: grid;
  gap: 7px;
  margin: 10px 0;
  color: var(--muted);
  font-size: 12px;
  font-weight: 700;
}
input,
select,
textarea {
  width: 100%;
  min-height: 40px;
  border: 1px solid #26384a;
  border-radius: 8px;
  background: rgba(5, 8, 13, .88);
  color: var(--fg);
  padding: 9px 11px;
}
input[readonly] {
  color: #c7fff9;
}
input:focus,
select:focus,
textarea:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-soft);
  outline: none;
}
textarea {
  min-height: 96px;
  resize: vertical;
}
code,
pre,
.lines code,
.advert-note-address,
.identity-output input {
  font-family: var(--mono);
}
.identity-tool {
  display: grid;
  gap: 12px;
}
.identity-output {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 10px;
  align-items: center;
}
.identity-output.secret-output {
  grid-template-columns: minmax(0, 1fr) auto auto;
}
.icon-button {
  width: 38px;
  padding: 0;
  display: inline-grid;
  place-items: center;
}
.icon-button svg {
  width: 18px;
  height: 18px;
  fill: none;
  stroke: currentColor;
  stroke-width: 2;
}
#status,
.status-box,
.song-test {
  display: grid;
  gap: 8px;
}
.status-line {
  display: grid;
  grid-template-columns: minmax(120px, .5fr) minmax(0, 1fr);
  gap: 12px;
  padding-bottom: 8px;
  border-bottom: 1px solid rgba(32, 48, 65, .8);
  font-size: 13px;
}
.status-line:last-child {
  padding-bottom: 0;
  border-bottom: 0;
}
.status-line-label {
  color: var(--muted);
  font-weight: 700;
}
.status-line-value {
  color: var(--fg);
  font-weight: 720;
  overflow-wrap: anywhere;
}
.status-line-value.ok { color: var(--accent); }
.status-line-value.bad { color: var(--danger); }
.status-line-value.neutral { color: var(--warn); }
.status-value {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--fg);
  font-weight: 800;
}
.status-value.ok { color: var(--accent); }
.status-value.bad { color: var(--danger); }
.ok {
  color: var(--accent);
  font-weight: 800;
}
.bad {
  color: var(--danger);
  font-weight: 800;
}
.field-links {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: -2px;
  font-size: 12px;
}
.field-link,
.footer-link,
.detail-link,
.site-footer a {
  color: var(--accent);
  text-decoration-color: rgba(25, 243, 255, .45);
  text-underline-offset: 3px;
}
.field-link[aria-disabled="true"] {
  color: var(--muted-2);
  pointer-events: none;
  text-decoration: none;
}
.site-footer {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 0 20px;
  color: var(--muted);
  font-size: 12px;
}
.footer-link {
  margin-right: auto;
  font-weight: 800;
  text-decoration: none;
}
.footer-meta {
  margin-left: auto;
  color: var(--muted-2);
  font-size: 11px;
  text-align: right;
}
.github-link {
  display: inline-grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border: 1px solid var(--line);
  border-radius: 999px;
  color: var(--muted);
  text-decoration: none;
}
.github-link:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.github-link svg {
  width: 18px;
  height: 18px;
  fill: currentColor;
}
.song-test audio {
  width: 100%;
}
.test-debug {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 8px;
  background: rgba(5, 8, 13, .9);
  color: var(--muted);
  overflow: auto;
  white-space: pre-wrap;
  font: 11px/1.4 var(--mono);
}
@media (max-width: 820px) {
  header {
    align-items: stretch;
    flex-direction: column;
  }
  .header-status {
    align-items: stretch;
    min-width: 0;
  }
  .toolbar,
  .actions,
  .form-actions {
    justify-content: flex-start;
  }
  .identity-output,
  .identity-output.secret-output,
  .status-line {
    grid-template-columns: 1fr;
  }
  .identity-output button {
    width: 100%;
  }
}
`

const commonJS = `
function statusClassFrom(value) {
  if (typeof value === "string") return value;
  return value ? "ok" : "bad";
}

function statusLine(label, value, state) {
  const row = document.createElement("div");
  row.className = "status-line";
  const left = document.createElement("span");
  left.className = "status-line-label";
  const right = document.createElement("span");
  right.className = "status-line-value";
  const stateClass = statusClassFrom(state);
  row.append(left, right);
  left.textContent = label;
  right.textContent = value;
  if (stateClass) right.classList.add(stateClass);
  return row;
}

function peerStatusFromCheck(peerCheck, peer = {}) {
  const check = peerCheck || {};
  const peerNpub = String(check.peer_npub || peer.npub || "").trim();
  const peerAddr = String(check.peer_addr || peer.addr || "").trim();
  if (!peerNpub && !peerAddr) {
    return { state: "neutral", text: "FIPS peer: not configured" };
  }
  if (!check.peer_npub && !check.peer_addr) {
    return {
      state: "neutral",
      text: peerAddr ? "FIPS peer: configured; outbound status pending" : "FIPS peer: identity configured; waiting for outbound session"
    };
  }
  if (check.transport_check_skipped || check.peer_addr_set === false) {
    return {
      state: "neutral",
      text: "FIPS peer: NAS identity configured; waiting for outbound session"
    };
  }
  if (check.error) {
    return {
      state: "bad",
      text: "FIPS peer: " + String(check.error) + (check.transport ? " (" + check.transport + ")" : "")
    };
  }
  if (check.reachable) {
    const transport = check.transport || "tcp";
    const addr = check.peer_addr || peerAddr;
    return { state: "ok", text: "FIPS peer: dialable via " + transport + (addr ? " (" + addr + ")" : "") };
  }
  if (peerAddr || peerNpub) {
    return { state: "bad", text: "FIPS peer: not dialable" };
  }
  return { state: "neutral", text: "FIPS peer: not configured" };
}

function renderSongTest(root, title, debug, audioURL, options = {}) {
  root.classList.remove("hidden");
  root.textContent = "";
  if (title) {
    if (options.titleMode === "status") {
      const line = document.createElement("div");
      line.className = "status";
      line.textContent = title;
      root.append(line);
    } else {
      root.appendChild(statusLine("Random song", title, audioURL ? true : "neutral"));
    }
  }
  if (audioURL) {
    const audio = document.createElement("audio");
    audio.controls = true;
    audio.autoplay = true;
    audio.src = audioURL;
    root.appendChild(audio);
    audio.play().catch(() => {});
  }
  const pre = document.createElement("pre");
  pre.className = "test-debug";
  pre.textContent = JSON.stringify(debug || [], null, 2);
  root.appendChild(pre);
}
`

// InjectShared replaces placeholders in admin HTML templates with shared
// browser-side CSS and JavaScript used by both admin surfaces.
func InjectShared(html string) string {
	html = strings.ReplaceAll(html, "{{ADMIN_COMMON_CSS}}", commonCSS)
	html = strings.ReplaceAll(html, "{{ADMIN_COMMON_JS}}", commonJS)
	return html
}
