package adminui

import "strings"

const commonCSS = `
.song-test {
  display: grid;
  gap: 8px;
}
.song-test audio { width: 100%; }
.test-debug {
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 8px;
  background: rgba(0, 0, 0, 0.03);
  overflow: auto;
  white-space: pre-wrap;
  font: 11px/1.4 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
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
